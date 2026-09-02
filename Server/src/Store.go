package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type articleAccumulator struct {
	ProjectID     string
	RoomID        string
	Board         string
	ArticleID     string
	Title         string
	Author        string
	RootMessageID string
	StartedAt     time.Time
	UpdatedAt     time.Time
	ReplyCount    int
}

type roomHistorySnapshot struct {
	ProjectID string
	RoomID    string
	RoomName  string
	Messages  []Message
}

type roomRefresh struct {
	done chan struct{}
	err  error
}

type Store struct {
	mu sync.RWMutex

	projects      map[string]*Project
	roomACLs      map[string]*ClientRoomACL
	agentRegistry map[string]*AgentRegistryEntry
	authTokens    map[string]*AuthTokenRecord

	dataDir       string
	maxInMemMsgs  int
	persistToDisk bool
	pg            *PostgresStore

	// single-flight room loading
	refreshMu sync.Mutex
	refreshes map[string]*roomRefresh

	// activity tracking (in-memory, best-effort)
	dayKey          string
	dailyMsgCount   int
	roomLastMsgAt   map[string]time.Time // key: project/room
	agentLastMsgAt  map[string]time.Time // key: agent id
	VisitorTracker  *VisitorTracker
	lastMessageTime time.Time

	securityMu              sync.RWMutex
	autoApprovalMu          sync.RWMutex
	autoApprovalEnabled     bool
	autoApprovalIntervalMin int
	allowedMCPOrigins       map[string]bool
	trustedProxyCIDRs   []*net.IPNet

	listenerMu sync.Mutex
	listeners  map[string]map[chan struct{}]struct{} // key: project/room
}

type Project struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Rooms map[string]*Room `json:"rooms,omitempty"`
}

type Room struct {
	mu          sync.RWMutex        `json:"-"`
	ID          string              `json:"id"`
	Board       string              `json:"board,omitempty"`
	Name        string              `json:"name"`
	Category    string              `json:"category,omitempty"`
	Description string              `json:"description,omitempty"`
	Owner       string              `json:"owner,omitempty"`
	Messages    []Message           `json:"messages,omitempty"`
	Presence    map[string]Presence `json:"presence,omitempty"`

	// Cache, Governance & Fast Pagination
	loaded               bool             `json:"-"`
	lastAccessAt         time.Time        `json:"-"`
	hitCount             uint64           `json:"-"`
	cachedSimpleArticles []ArticleSummary `json:"-"`
	sigAcc               uint64           `json:"-"`
}

func computeMessageSig(m Message) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(m.ID))
	_, _ = h.Write([]byte(m.Title))
	_, _ = h.Write([]byte(m.Text))
	_, _ = h.Write([]byte(m.Author))
	_, _ = h.Write([]byte(m.DisplayName))
	_, _ = h.Write([]byte(strconv.FormatInt(m.TS.UnixNano(), 10)))
	return h.Sum64()
}

func (r *Room) touchLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	r.lastAccessAt = now
	r.hitCount++
}

func (r *Room) Signature() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sigAcc ^ (uint64(len(r.Messages)) * 0x9e3779b97f4a7c15)
}

func (r *Room) Stats() (loaded bool, msgCount int, hitCount uint64, lastAccess time.Time) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded, len(r.Messages), r.hitCount, r.lastAccessAt
}

type Presence struct {
	AgentID  string    `json:"agent_id"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

type Message struct {
	ID               string         `json:"id"`
	MessageID        string         `json:"message,omitempty"`
	ProjectID        string         `json:"project_id"`
	RoomID           string         `json:"room_id"`
	Board            string         `json:"board,omitempty"`
	AgentID          string         `json:"agent_id"`
	DisplayName      string         `json:"display_name,omitempty"`
	Author           string         `json:"author,omitempty"`
	ArticleID        string         `json:"article_id,omitempty"`
	Article          string         `json:"article,omitempty"`
	Title            string         `json:"title,omitempty"`
	ReplyToMessageID string         `json:"reply_to_message_id,omitempty"`
	ReplyToMessage   string         `json:"reply_to_message,omitempty"`
	Text             string         `json:"text"`
	TS               time.Time      `json:"ts"`
	Meta             map[string]any `json:"meta,omitempty"`
}

var (
	ErrProjectNotFound = errors.New("project not found")
	ErrRoomNotFound    = errors.New("room not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrMissingClientID = errors.New("missing client id")
	ErrForbidden       = errors.New("forbidden")
	ErrMessageNotFound = errors.New("message not found")
)

func (s *Store) ConfigureSecurity(origins, trustedProxyCIDRs []string) error {
	if s == nil {
		return fmt.Errorf("store not available")
	}
	allowed := make(map[string]bool)
	for _, origin := range origins {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin == "" {
			continue
		}
		if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
			return fmt.Errorf("invalid MCP origin %q", origin)
		}
		allowed[origin] = true
	}
	// An empty origin list disables the MCP origin allowlist.
	cidrs := make([]*net.IPNet, 0, len(trustedProxyCIDRs))
	for _, raw := range trustedProxyCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
		}
		cidrs = append(cidrs, network)
	}
	s.securityMu.Lock()
	s.allowedMCPOrigins = allowed
	s.trustedProxyCIDRs = cidrs
	s.securityMu.Unlock()
	return nil
}

func NewStore(dataDir string, maxInMemMsgs int, persist bool) *Store {
	store, err := NewStoreWithError(dataDir, maxInMemMsgs, persist)
	if err != nil {
		return store
	}
	return store
}

func NewStoreWithError(dataDir string, maxInMemMsgs int, persist bool) (*Store, error) {
	if maxInMemMsgs <= 0 {
		maxInMemMsgs = 200
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	store := &Store{
		projects: make(map[string]*Project), roomACLs: make(map[string]*ClientRoomACL),
		agentRegistry: make(map[string]*AgentRegistryEntry), authTokens: make(map[string]*AuthTokenRecord),
		dataDir: dataDir, maxInMemMsgs: maxInMemMsgs, persistToDisk: persist,
		roomLastMsgAt: make(map[string]time.Time), agentLastMsgAt: make(map[string]time.Time),
		dayKey: time.Now().Format("2006-01-02"), allowedMCPOrigins: defaultMCPOrigins(),
		VisitorTracker: NewVisitorTracker(dataDir),
	}
	if err := store.LoadACLs(); err != nil {
		return nil, fmt.Errorf("load ACLs: %w", err)
	}
	if err := store.LoadRegistry(); err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	if err := store.LoadAuthTokens(); err != nil {
		return nil, fmt.Errorf("load auth tokens: %w", err)
	}
	if err := store.LoadAutoApprovalConfig(); err != nil {
		return nil, fmt.Errorf("load auto approval config: %w", err)
	}
	if err := store.LoadMessagesFromDisk(); err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}
	return store, nil
}

func NewStoreWithPostgres(pg *PostgresStore, maxInMemMsgs int) (*Store, error) {
	if pg == nil {
		return nil, fmt.Errorf("postgres store cannot be nil")
	}
	if maxInMemMsgs <= 0 {
		maxInMemMsgs = 200
	}
	store := &Store{
		projects:          make(map[string]*Project),
		roomACLs:          make(map[string]*ClientRoomACL),
		agentRegistry:     make(map[string]*AgentRegistryEntry),
		authTokens:        make(map[string]*AuthTokenRecord),
		dataDir:           "",
		maxInMemMsgs:      maxInMemMsgs,
		persistToDisk:     false,
		roomLastMsgAt:     make(map[string]time.Time),
		agentLastMsgAt:    make(map[string]time.Time),
		dayKey:            time.Now().Format("2006-01-02"),
		allowedMCPOrigins: defaultMCPOrigins(),
		pg:                pg,
		VisitorTracker:    NewVisitorTracker("./data"),
	}

	if err := store.LoadFromPostgres(); err != nil {
		return nil, fmt.Errorf("load data from postgres: %w", err)
	}
	return store, nil
}

func (s *Store) LoadFromPostgres() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pg == nil {
		return fmt.Errorf("postgres not connected")
	}

	// 1. Load Boards from PostgreSQL
	pgBoards, err := s.pg.LoadAllBoards()
	if err != nil {
		return fmt.Errorf("load boards: %w", err)
	}

	for _, b := range pgBoards {
		pid := firstNonEmpty(b.ProjectID, "default")
		p, ok := s.projects[pid]
		if !ok {
			p = &Project{ID: pid, Name: pid, Rooms: make(map[string]*Room)}
			s.projects[pid] = p
		}
		r := &Room{
			ID:          b.RoomID,
			Board:       b.RoomID,
			Name:        b.Name,
			Category:    b.Category,
			Description: b.Description,
			Owner:       b.Owner,
			Messages:    make([]Message, 0),
			Presence:    make(map[string]Presence),
		}
		p.Rooms[b.RoomID] = r

		limit := s.maxInMemMsgs
		if limit <= 0 {
			limit = 2000
		}
		msgs, err := s.pg.LoadMessagesForRoom(pid, b.RoomID, limit)
		if err == nil && len(msgs) > 0 {
			r.Messages = msgs
			lastMsg := msgs[len(msgs)-1]
			s.roomLastMsgAt[pid+"/"+b.RoomID] = lastMsg.TS
			if lastMsg.TS.After(s.lastMessageTime) {
				s.lastMessageTime = lastMsg.TS
			}
		}
	}

	// 2. Load Agent Registry from PostgreSQL
	pgRegistry, err := s.pg.LoadAllAgentRegistry()
	if err == nil && pgRegistry != nil {
		s.agentRegistry = pgRegistry
	}

	// 3. Load Room ACLs from PostgreSQL
	pgACLs, err := s.pg.LoadAllRoomACLs()
	if err == nil && pgACLs != nil {
		s.roomACLs = pgACLs
	}

	// 4. Load Auth Tokens from PostgreSQL
	pgTokens, err := s.pg.LoadAllAuthTokens()
	if err == nil && pgTokens != nil {
		s.authTokens = pgTokens
	}

	// 5. Load Presence from PostgreSQL
	pgPresence, err := s.pg.LoadAllPresence()
	if err == nil && pgPresence != nil {
		for key, presMap := range pgPresence {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) == 2 {
				pid, rid := parts[0], parts[1]
				if p, ok := s.projects[pid]; ok {
					if r, ok := p.Rooms[rid]; ok {
						for aid, pr := range presMap {
							r.Presence[aid] = pr
						}
					}
				}
			}
		}
	}

	// 6. Load Auto Approval Config
	_ = s.LoadAutoApprovalConfig()

	return nil
}

func (s *Store) SetPostgres(pg *PostgresStore) error {
	if s == nil || pg == nil {
		return nil
	}
	s.mu.Lock()
	s.pg = pg
	s.mu.Unlock()

	return s.syncWithPostgres()
}

func (s *Store) syncWithPostgres() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pg == nil {
		return nil
	}

	// 1. Sync Boards & Messages from PostgreSQL
	pgBoards, err := s.pg.LoadAllBoards()
	if err == nil && len(pgBoards) > 0 {
		for _, b := range pgBoards {
			pid := firstNonEmpty(b.ProjectID, "default")
			p, ok := s.projects[pid]
			if !ok {
				p = &Project{ID: pid, Name: pid, Rooms: make(map[string]*Room)}
				s.projects[pid] = p
			}
			r, exists := p.Rooms[b.RoomID]
			if !exists {
				r = &Room{
					ID:          b.RoomID,
					Board:       b.RoomID,
					Name:        b.Name,
					Category:    b.Category,
					Description: b.Description,
					Owner:       b.Owner,
					Messages:    make([]Message, 0),
					Presence:    make(map[string]Presence),
				}
				p.Rooms[b.RoomID] = r
			} else {
				if r.Name == "" || (r.Name == r.ID && b.Name != "") {
					r.Name = b.Name
				}
				if r.Category == "" {
					r.Category = b.Category
				}
				if r.Description == "" {
					r.Description = b.Description
				}
				if r.Owner == "" {
					r.Owner = b.Owner
				}
			}
			msgs, _ := s.pg.LoadMessagesForRoom(pid, b.RoomID, 2000)
			if len(msgs) > 0 {
				r.Messages = msgs
			}
		}
	}

	// For any memory boards not yet in PG, save them to PG
	for pid, p := range s.projects {
		for rid, r := range p.Rooms {
			_ = s.pg.SaveBoardMetadata(pid, rid, r.Name, r.Category, r.Description, r.Owner)
			for _, m := range r.Messages {
				_ = s.pg.InsertMessage(m)
			}
		}
	}

	// 2. Sync Agent Registry
	pgRegistry, err := s.pg.LoadAllAgentRegistry()
	if err == nil && len(pgRegistry) > 0 {
		for k, v := range pgRegistry {
			s.agentRegistry[k] = v
		}
	}
	for _, entry := range s.agentRegistry {
		_ = s.pg.SaveAgentRegistryEntry(entry)
	}

	// 3. Sync ACLs
	pgACLs, err := s.pg.LoadAllRoomACLs()
	if err == nil && len(pgACLs) > 0 {
		for k, v := range pgACLs {
			s.roomACLs[k] = v
		}
	}
	for cid, acl := range s.roomACLs {
		_ = s.pg.SaveRoomACL(cid, acl.Allow, acl.Deny)
	}

	// 4. Sync Auth Tokens
	pgTokens, err := s.pg.LoadAllAuthTokens()
	if err == nil && len(pgTokens) > 0 {
		for k, v := range pgTokens {
			s.authTokens[k] = v
		}
	}
	for _, tok := range s.authTokens {
		_ = s.pg.SaveAuthToken(tok)
	}

	// 5. Sync Presence
	pgPresence, err := s.pg.LoadAllPresence()
	if err == nil && len(pgPresence) > 0 {
		for key, presMap := range pgPresence {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) == 2 {
				pid, rid := parts[0], parts[1]
				if p, ok := s.projects[pid]; ok {
					if r, ok := p.Rooms[rid]; ok {
						for aid, pr := range presMap {
							r.Presence[aid] = pr
						}
					}
				}
			}
		}
	}

	// 6. Sync Auto Approval Config
	_ = s.SaveAutoApprovalConfig()

	return nil
}

func (s *Store) HasProject(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.projects[id]
	return ok
}

func (s *Store) HasRoom(projectID, roomID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[projectID]
	if !ok {
		return false
	}
	_, ok = p.Rooms[roomID]
	return ok
}

func (s *Store) ResolveBoardProjectID(boardID string) (string, bool) {
	boardID = strings.TrimSpace(boardID)
	if boardID == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for projectID, project := range s.projects {
		if project == nil {
			continue
		}
		if _, ok := project.Rooms[boardID]; ok {
			return projectID, true
		}
	}
	return "", false
}

func (s *Store) CreateProject(id, name string) (*Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[id]; ok {
		return nil, ErrAlreadyExists
	}
	if name == "" {
		name = id
	}
	p := &Project{ID: id, Name: name, Rooms: make(map[string]*Room)}
	s.projects[id] = p
	s.saveProjectMetaLocked(p)
	return p, nil
}

func (s *Store) GetProject(id string) (*Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, false
	}
	cp := &Project{ID: p.ID, Name: p.Name}
	return cp, true
}

func (s *Store) CreateRoom(projectID, roomID, name, category, description, owner string) (*Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[projectID]
	if !ok {
		p = &Project{ID: projectID, Name: projectID, Rooms: make(map[string]*Room)}
		s.projects[projectID] = p
		s.saveProjectMetaLocked(p)
	}
	if _, ok := p.Rooms[roomID]; ok {
		return nil, ErrAlreadyExists
	}
	if name == "" {
		name = roomID
	}
	r := &Room{
		ID:           roomID,
		Board:        roomID,
		Name:         name,
		Category:     category,
		Description:  description,
		Owner:        owner,
		Presence:     make(map[string]Presence),
		Messages:     make([]Message, 0),
		loaded:       true,
		lastAccessAt: time.Now(),
	}
	p.Rooms[roomID] = r
	if err := s.saveRoomMetaLocked(projectID, r); err != nil {
		delete(p.Rooms, roomID)
		return nil, err
	}
	return &Room{ID: r.ID, Board: roomID, Name: r.Name, Category: r.Category, Description: r.Description, Owner: r.Owner}, nil
}

func (s *Store) UpdateRoom(projectID, roomID, name, category, description, owner string) (*Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[projectID]
	if !ok {
		return nil, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		return nil, ErrRoomNotFound
	}
	r.Board = roomID
	if strings.TrimSpace(name) != "" {
		r.Name = strings.TrimSpace(name)
	}
	r.Category = strings.TrimSpace(category)
	r.Description = strings.TrimSpace(description)
	r.Owner = strings.TrimSpace(owner)
	if err := s.saveRoomMetaLocked(projectID, r); err != nil {
		return nil, err
	}
	return &Room{ID: r.ID, Board: roomID, Name: r.Name, Category: r.Category, Description: r.Description, Owner: r.Owner}, nil
}

func (s *Store) DeleteRoom(projectID, roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[projectID]
	if !ok {
		return ErrProjectNotFound
	}
	if _, ok := p.Rooms[roomID]; !ok {
		return ErrRoomNotFound
	}
	delete(p.Rooms, roomID)
	delete(s.roomLastMsgAt, projectID+"/"+roomID)
	if s.pg != nil {
		_ = s.pg.DeleteBoardMetadata(projectID, roomID)
	}
	if s.dataDir != "" {
		_ = os.RemoveAll(s.boardDir(projectID, roomID))
	}
	return nil
}

func (s *Store) GetRoom(projectID, roomID string) (*Room, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[projectID]
	if !ok {
		return nil, false
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := &Room{
		ID:           r.ID,
		Board:        r.ID,
		Name:         r.Name,
		Category:     r.Category,
		Description:  r.Description,
		Owner:        r.Owner,
		loaded:       r.loaded,
		sigAcc:       r.sigAcc,
		lastAccessAt: r.lastAccessAt,
		hitCount:     r.hitCount,
		Messages:     append([]Message(nil), r.Messages...),
	}
	return cp, true
}

func (s *Store) GetRoomSignature(projectID, roomID string) (uint64, error) {
	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return 0, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return 0, ErrRoomNotFound
	}
	s.mu.RUnlock()

	return r.Signature(), nil
}

func (s *Store) ListProjects() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		out = append(out, Project{ID: p.ID, Name: p.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) ListRooms(projectID string) ([]Room, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.projects[projectID]
	if !ok {
		return nil, ErrProjectNotFound
	}
	out := make([]Room, 0, len(p.Rooms))
	for _, r := range p.Rooms {
		out = append(out, Room{ID: r.ID, Board: r.ID, Name: r.Name, Category: r.Category, Description: r.Description, Owner: r.Owner})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) normalizeMessageAliases(m *Message) {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.RoomID) == "" && strings.TrimSpace(m.Board) != "" {
		m.RoomID = strings.TrimSpace(m.Board)
	}
	if strings.TrimSpace(m.Board) == "" && strings.TrimSpace(m.RoomID) != "" {
		m.Board = strings.TrimSpace(m.RoomID)
	}
	if strings.TrimSpace(m.ArticleID) == "" && strings.TrimSpace(m.Article) != "" {
		m.ArticleID = strings.TrimSpace(m.Article)
	}
	if strings.TrimSpace(m.Article) == "" && strings.TrimSpace(m.ArticleID) != "" {
		m.Article = strings.TrimSpace(m.ArticleID)
	}
	if strings.TrimSpace(m.MessageID) == "" && strings.TrimSpace(m.ID) != "" {
		m.MessageID = strings.TrimSpace(m.ID)
	}
	if strings.TrimSpace(m.ReplyToMessageID) == "" && strings.TrimSpace(m.ReplyToMessage) != "" {
		m.ReplyToMessageID = strings.TrimSpace(m.ReplyToMessage)
	}
	if strings.TrimSpace(m.ReplyToMessage) == "" && strings.TrimSpace(m.ReplyToMessageID) != "" {
		m.ReplyToMessage = strings.TrimSpace(m.ReplyToMessageID)
	}
	if s != nil {
		name := s.resolveAuthorNameLocked(m.AgentID)
		if name != "" {
			m.Author = name
			m.DisplayName = name
		}
	}
}

func (s *Store) AddMessage(m Message) error {
	now := time.Now()
	if m.TS.IsZero() {
		m.TS = now
	}
	m.ArticleID = strings.TrimSpace(m.ArticleID)
	m.Article = strings.TrimSpace(m.Article)
	m.Title = strings.TrimSpace(m.Title)
	m.ReplyToMessageID = strings.TrimSpace(m.ReplyToMessageID)
	m.ReplyToMessage = strings.TrimSpace(m.ReplyToMessage)
	if m.ArticleID == "" {
		m.ArticleID = m.Article
	}
	if m.ReplyToMessageID == "" {
		m.ReplyToMessageID = m.ReplyToMessage
	}

	_ = s.EnsureRoomLoaded(m.ProjectID, m.RoomID)

	s.mu.RLock()
	p, ok := s.projects[m.ProjectID]
	if !ok {
		s.mu.RUnlock()
		return ErrProjectNotFound
	}
	r, ok := p.Rooms[m.RoomID]
	if !ok {
		s.mu.RUnlock()
		return ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.Lock()
	if m.ArticleID == "" {
		if m.ReplyToMessageID != "" {
			for i := len(r.Messages) - 1; i >= 0; i-- {
				parent := r.Messages[i]
				if parent.ID != m.ReplyToMessageID {
					continue
				}
				if parent.ArticleID != "" {
					m.ArticleID = parent.ArticleID
				} else {
					m.ArticleID = parent.ID
				}
				if m.Title == "" {
					m.Title = parent.Title
				}
				break
			}
		}
		if m.ArticleID == "" {
			m.ArticleID = m.ID
		}
	}
	s.normalizeMessageAliases(&m)

	r.Messages = append(r.Messages, m)
	if s.maxInMemMsgs > 0 && len(r.Messages) > s.maxInMemMsgs {
		r.Messages = r.Messages[len(r.Messages)-s.maxInMemMsgs:]
	}
	r.cachedSimpleArticles = nil
	r.sigAcc ^= computeMessageSig(m)
	r.touchLocked(now)
	r.mu.Unlock()

	s.mu.Lock()
	day := now.Format("2006-01-02")
	if s.dayKey != day {
		s.dayKey = day
		s.dailyMsgCount = 0
	}
	s.dailyMsgCount++

	key := m.ProjectID + "/" + m.RoomID
	s.roomLastMsgAt[key] = now
	s.agentLastMsgAt[m.AgentID] = now
	if now.After(s.lastMessageTime) {
		s.lastMessageTime = now
	}
	s.mu.Unlock()

	if s.persistToDisk {
		if err := s.appendMessageToDisk(m); err != nil {
			return err
		}
	}
	if s.pg != nil {
		_ = s.pg.InsertMessage(m)
	}
	s.notifyRoomListeners(m.ProjectID, m.RoomID)
	return nil
}

func (s *Store) SubscribeRoom(projectID, roomID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	if s == nil {
		return ch, func() {}
	}
	key := strings.TrimSpace(projectID) + "/" + strings.TrimSpace(roomID)
	s.listenerMu.Lock()
	if s.listeners == nil {
		s.listeners = make(map[string]map[chan struct{}]struct{})
	}
	if s.listeners[key] == nil {
		s.listeners[key] = make(map[chan struct{}]struct{})
	}
	s.listeners[key][ch] = struct{}{}
	s.listenerMu.Unlock()

	cancel := func() {
		s.listenerMu.Lock()
		if s.listeners != nil && s.listeners[key] != nil {
			delete(s.listeners[key], ch)
			if len(s.listeners[key]) == 0 {
				delete(s.listeners, key)
			}
		}
		s.listenerMu.Unlock()
	}
	return ch, cancel
}

func (s *Store) notifyRoomListeners(projectID, roomID string) {
	if s == nil {
		return
	}
	key := strings.TrimSpace(projectID) + "/" + strings.TrimSpace(roomID)
	s.listenerMu.Lock()
	if s.listeners == nil || len(s.listeners[key]) == 0 {
		s.listenerMu.Unlock()
		return
	}
	for ch := range s.listeners[key] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	s.listenerMu.Unlock()
}

func (s *Store) appendMessageToDisk(m Message) error {
	dir := s.boardDir(m.ProjectID, m.RoomID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "messages.jsonl")

	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) roomMessagesPath(projectID, roomID string) string {
	return filepath.Join(s.boardDir(projectID, roomID), "messages.jsonl")
}

type roomMetaDisk struct {
	ProjectID   string              `json:"project_id,omitempty"`
	ID          string              `json:"id"`
	Board       string              `json:"board,omitempty"`
	Name        string              `json:"name"`
	Category    string              `json:"category,omitempty"`
	Description string              `json:"description,omitempty"`
	Owner       string              `json:"owner,omitempty"`
	Presence    map[string]Presence `json:"presence,omitempty"`
}

type projectMetaDisk struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Store) boardDir(projectID, roomID string) string {
	return filepath.Join(s.dataDir, "boards", safeStorageComponent(projectID), safeStorageComponent(roomID))
}

func safeStorageComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range value {
		fmt.Fprintf(&b, "%x", r)
	}
	return b.String()
}

func clonePresence(in map[string]Presence) map[string]Presence {
	if len(in) == 0 {
		return make(map[string]Presence)
	}
	out := make(map[string]Presence, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Store) saveProjectMetaLocked(project *Project) {
	_ = project
}

func (s *Store) loadProjectMeta(projectID string) projectMetaDisk {
	_ = projectID
	return projectMetaDisk{}
}

func (s *Store) roomMetaPath(projectID, roomID string) string {
	return filepath.Join(s.boardDir(projectID, roomID), "board.json")
}

func (s *Store) saveRoomMetaLocked(projectID string, room *Room) error {
	if room == nil {
		return nil
	}
	path := s.roomMetaPath(projectID, room.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(roomMetaDisk{
		ProjectID:   projectID,
		ID:          room.ID,
		Board:       room.ID,
		Name:        room.Name,
		Category:    room.Category,
		Description: room.Description,
		Owner:       room.Owner,
		Presence:    room.Presence,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".board-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if s.pg != nil {
		_ = s.pg.SaveBoardMetadata(projectID, room.ID, room.Name, room.Category, room.Description, room.Owner)
	}
	return os.Rename(tmpName, path)
}

func (s *Store) loadRoomMeta(projectID, roomID string) roomMetaDisk {
	path := s.roomMetaPath(projectID, roomID)
	b, err := os.ReadFile(path)
	if err != nil && projectID != "" {
		// Read-only compatibility with the former boards/<room> layout.
		b, err = os.ReadFile(filepath.Join(s.dataDir, "boards", safeStorageComponent(roomID), "board.json"))
	}
	if err != nil {
		return roomMetaDisk{}
	}
	var meta roomMetaDisk
	if err := json.Unmarshal(b, &meta); err != nil {
		return roomMetaDisk{}
	}
	return meta
}

// MigrateLegacyBoards moves legacy boards/<room> directories into the
// project-isolated boards/<project>/<room> layout. It never overwrites an
// existing destination and refuses metadata without an explicit project_id.
func MigrateLegacyBoards(dataDir string) error {
	root := filepath.Join(dataDir, "boards")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		source := filepath.Join(root, entry.Name())
		if !hasBoardFiles(source) {
			continue
		}
		meta := readRoomMetaFile(filepath.Join(source, "board.json"))
		projectID := strings.TrimSpace(meta.ProjectID)
		roomID := strings.TrimSpace(meta.ID)
		if projectID == "" {
			return fmt.Errorf("legacy board %q has no project_id", entry.Name())
		}
		if roomID == "" {
			roomID = entry.Name()
		}
		destination := filepath.Join(root, safeStorageComponent(projectID), safeStorageComponent(roomID))
		if _, err := os.Stat(destination); err == nil {
			return fmt.Errorf("legacy board %q destination already exists: %s", entry.Name(), destination)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("migrate legacy board %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) LoadMessagesFromDisk() error {
	root := filepath.Join(s.dataDir, "boards")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, projectEntry := range entries {
		if !projectEntry.IsDir() {
			continue
		}
		projectPath := filepath.Join(root, projectEntry.Name())
		rooms, err := os.ReadDir(projectPath)
		if err != nil {
			return err
		}
		// Legacy boards/<room> directory: metadata identifies its project.
		if hasBoardFiles(projectPath) {
			meta := readRoomMetaFile(filepath.Join(projectPath, "board.json"))
			projectID, roomID := strings.TrimSpace(meta.ProjectID), strings.TrimSpace(meta.ID)
			if roomID == "" {
				roomID = projectEntry.Name()
			}
			if projectID == "" {
				projectID = defaultLobbyProjectID
			}
			s.ensureProjectLoaded(projectID)
			s.ensureRoomLoaded(projectID, roomID)
			if err := s.loadRoomMessagesFromDisk(projectID, roomID, filepath.Join(projectPath, "messages.jsonl")); err != nil {
				return err
			}
			continue
		}
		for _, roomEntry := range rooms {
			if !roomEntry.IsDir() {
				continue
			}
			roomPath := filepath.Join(projectPath, roomEntry.Name())
			meta := readRoomMetaFile(filepath.Join(roomPath, "board.json"))
			projectID, roomID := strings.TrimSpace(meta.ProjectID), strings.TrimSpace(meta.ID)
			if projectID == "" {
				projectID = projectEntry.Name()
			}
			if roomID == "" {
				roomID = roomEntry.Name()
			}
			s.ensureProjectLoaded(projectID)
			s.ensureRoomLoaded(projectID, roomID)
			if err := s.loadRoomMessagesFromDisk(projectID, roomID, filepath.Join(roomPath, "messages.jsonl")); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasBoardFiles(path string) bool {
	_, metaErr := os.Stat(filepath.Join(path, "board.json"))
	_, messagesErr := os.Stat(filepath.Join(path, "messages.jsonl"))
	return metaErr == nil || messagesErr == nil
}

func readRoomMetaFile(path string) roomMetaDisk {
	b, err := os.ReadFile(path)
	if err != nil {
		return roomMetaDisk{}
	}
	var meta roomMetaDisk
	if json.Unmarshal(b, &meta) != nil {
		return roomMetaDisk{}
	}
	return meta
}

func (s *Store) ensureProjectLoaded(projectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; ok {
		return
	}
	meta := s.loadProjectMeta(projectID)
	name := projectID
	if strings.TrimSpace(meta.Name) != "" {
		name = strings.TrimSpace(meta.Name)
	}
	s.projects[projectID] = &Project{
		ID:    projectID,
		Name:  name,
		Rooms: make(map[string]*Room),
	}
}

func (s *Store) ensureRoomLoaded(projectID, roomID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		meta := s.loadProjectMeta(projectID)
		name := projectID
		if strings.TrimSpace(meta.Name) != "" {
			name = strings.TrimSpace(meta.Name)
		}
		project = &Project{
			ID:    projectID,
			Name:  name,
			Rooms: make(map[string]*Room),
		}
		s.projects[projectID] = project
	}
	if _, ok := project.Rooms[roomID]; ok {
		project.Rooms[roomID].Board = roomID
		return
	}
	meta := s.loadRoomMeta(projectID, roomID)
	name := roomID
	if strings.TrimSpace(meta.Name) != "" {
		name = strings.TrimSpace(meta.Name)
	}
	project.Rooms[roomID] = &Room{
		ID:          roomID,
		Board:       roomID,
		Name:        name,
		Category:    strings.TrimSpace(meta.Category),
		Description: strings.TrimSpace(meta.Description),
		Owner:       strings.TrimSpace(meta.Owner),
		Presence:    clonePresence(meta.Presence),
		Messages:    []Message{},
	}
}

func (s *Store) loadRoomMessagesFromDisk(projectID, roomID, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	msgs := make([]Message, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.ProjectID == "" {
			msg.ProjectID = projectID
		}
		if msg.RoomID == "" {
			msg.RoomID = roomID
		}
		if msg.RoomID == "" && strings.TrimSpace(msg.Board) != "" {
			msg.RoomID = strings.TrimSpace(msg.Board)
		}
		if msg.ProjectID != projectID || msg.RoomID != roomID {
			continue
		}
		if msg.ArticleID == "" && strings.TrimSpace(msg.Article) != "" {
			msg.ArticleID = strings.TrimSpace(msg.Article)
		}
		if msg.ArticleID == "" {
			if msg.ReplyToMessageID != "" {
				for i := len(msgs) - 1; i >= 0; i-- {
					parent := msgs[i]
					if parent.ID != msg.ReplyToMessageID {
						continue
					}
					if parent.ArticleID != "" {
						msg.ArticleID = parent.ArticleID
					} else {
						msg.ArticleID = parent.ID
					}
					if msg.Title == "" {
						msg.Title = parent.Title
					}
					break
				}
			}
			if msg.ArticleID == "" {
				msg.ArticleID = msg.ID
			}
		}
		if msg.ReplyToMessageID == "" && strings.TrimSpace(msg.ReplyToMessage) != "" {
			msg.ReplyToMessageID = strings.TrimSpace(msg.ReplyToMessage)
		}
		s.normalizeMessageAliases(&msg)
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	if s.maxInMemMsgs > 0 && len(msgs) > s.maxInMemMsgs {
		msgs = msgs[len(msgs)-s.maxInMemMsgs:]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	meta := s.loadRoomMeta(projectID, roomID)

	project, ok := s.projects[projectID]
	if !ok {
		project = &Project{ID: projectID, Name: projectID, Rooms: make(map[string]*Room)}
		s.projects[projectID] = project
	}
	room, ok := project.Rooms[roomID]
	if !ok {
		roomName := roomID
		if meta.Name != "" {
			roomName = meta.Name
		}
		room = &Room{
			ID:          roomID,
			Board:       roomID,
			Name:        roomName,
			Category:    meta.Category,
			Description: meta.Description,
			Owner:       meta.Owner,
			Presence:    clonePresence(meta.Presence),
		}
		project.Rooms[roomID] = room
	} else {
		room.Board = roomID
		if meta.Name != "" {
			room.Name = meta.Name
		}
		room.Category = meta.Category
		room.Description = meta.Description
		room.Owner = meta.Owner
		room.Presence = clonePresence(meta.Presence)
	}
	room.Messages = append([]Message(nil), msgs...)
	room.loaded = true
	room.cachedSimpleArticles = nil
	room.sigAcc = 0
	for _, m := range msgs {
		room.sigAcc ^= computeMessageSig(m)
	}
	room.touchLocked(time.Now())

	for _, msg := range msgs {
		if msg.TS.IsZero() {
			continue
		}
		day := msg.TS.Format("2006-01-02")
		if day == s.dayKey {
			s.dailyMsgCount++
		}
		roomKey := msg.ProjectID + "/" + msg.RoomID
		if msg.TS.After(s.roomLastMsgAt[roomKey]) {
			s.roomLastMsgAt[roomKey] = msg.TS
		}
		if msg.TS.After(s.agentLastMsgAt[msg.AgentID]) {
			s.agentLastMsgAt[msg.AgentID] = msg.TS
		}
		if msg.TS.After(s.lastMessageTime) {
			s.lastMessageTime = msg.TS
		}
	}

	return nil
}

func (s *Store) readMessagesFromDisk(projectID, roomID, path string, limit int) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	msgs := make([]Message, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.ProjectID == "" {
			msg.ProjectID = projectID
		}
		if msg.RoomID == "" {
			msg.RoomID = roomID
		}
		if msg.ProjectID != projectID || msg.RoomID != roomID {
			continue
		}
		s.normalizeMessageAliases(&msg)
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func (s *Store) EnsureRoomLoaded(projectID, roomID string) error {
	if s == nil {
		return nil
	}
	projectID = firstNonEmpty(projectID, "default")
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil
	}

	s.mu.RLock()
	p, pOk := s.projects[projectID]
	if !pOk {
		s.mu.RUnlock()
		return ErrProjectNotFound
	}
	r, rOk := p.Rooms[roomID]
	if !rOk {
		s.mu.RUnlock()
		return ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.RLock()
	loaded := r.loaded
	r.mu.RUnlock()
	if loaded {
		return nil
	}

	key := projectID + "/" + roomID
	s.refreshMu.Lock()
	if s.refreshes == nil {
		s.refreshes = make(map[string]*roomRefresh)
	}
	refresh, ok := s.refreshes[key]
	if !ok {
		refresh = &roomRefresh{done: make(chan struct{})}
		s.refreshes[key] = refresh
		s.refreshMu.Unlock()

		go func() {
			var loadErr error
			defer func() {
				if rec := recover(); rec != nil {
					loadErr = fmt.Errorf("load room panic: %v", rec)
				}
				refresh.err = loadErr
				close(refresh.done)
				s.refreshMu.Lock()
				delete(s.refreshes, key)
				s.refreshMu.Unlock()
			}()

			limit := s.maxInMemMsgs
			if limit <= 0 {
				limit = 2000
			}

			var msgs []Message
			if s.pg != nil {
				msgs, loadErr = s.pg.LoadMessagesForRoom(projectID, roomID, limit)
			} else if s.dataDir != "" {
				boardPath := s.boardDir(projectID, roomID)
				msgsPath := filepath.Join(boardPath, "messages.jsonl")
				msgs, loadErr = s.readMessagesFromDisk(projectID, roomID, msgsPath, limit)
			}

			if loadErr == nil {
				r.mu.Lock()
				r.Messages = msgs
				r.loaded = true
				r.cachedSimpleArticles = nil
				r.sigAcc = 0
				for _, m := range msgs {
					r.sigAcc ^= computeMessageSig(m)
				}
				r.touchLocked(time.Now())
				r.mu.Unlock()
			}
		}()
	} else {
		s.refreshMu.Unlock()
	}

	<-refresh.done
	return refresh.err
}

type RoomReleaseStats struct {
	ProjectID string
	RoomID    string
	Released  bool
	Messages  int
}

func (s *Store) ReleaseRoomMemory(projectID, roomID string) (RoomReleaseStats, error) {
	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return RoomReleaseStats{}, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return RoomReleaseStats{}, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	stats := RoomReleaseStats{
		ProjectID: projectID,
		RoomID:    roomID,
		Messages:  len(r.Messages),
	}
	if r.loaded && len(r.Messages) > 0 {
		r.Messages = nil
		r.cachedSimpleArticles = nil
		r.loaded = false
		stats.Released = true
	}
	return stats, nil
}

func (s *Store) EvictIdleRooms(idleTimeout time.Duration) int {
	if idleTimeout <= 0 {
		idleTimeout = 30 * time.Minute
	}
	cutoff := time.Now().Add(-idleTimeout)

	s.mu.RLock()
	type targetRoom struct {
		pid string
		rid string
		r   *Room
	}
	var targets []targetRoom
	for pid, p := range s.projects {
		for rid, r := range p.Rooms {
			targets = append(targets, targetRoom{pid: pid, rid: rid, r: r})
		}
	}
	s.mu.RUnlock()

	releasedCount := 0
	for _, t := range targets {
		t.r.mu.Lock()
		if t.r.loaded && len(t.r.Messages) > 0 && !t.r.lastAccessAt.IsZero() && t.r.lastAccessAt.Before(cutoff) {
			t.r.Messages = nil
			t.r.cachedSimpleArticles = nil
			t.r.loaded = false
			releasedCount++
		}
		t.r.mu.Unlock()
	}
	return releasedCount
}

func (s *Store) resolveAuthorNameLocked(agentID string) string {
	raw := strings.TrimSpace(agentID)
	if raw == "" {
		return ""
	}
	id := raw
	if idx := strings.Index(raw, ":"); idx >= 0 {
		id = strings.TrimSpace(raw[idx+1:])
	}
	if s != nil {
		if entry, ok := s.agentRegistry[id]; ok {
			if name := strings.TrimSpace(entry.DisplayName); name != "" {
				return name
			}
		}
	}
	return raw
}

func normalizeAuthorIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.Index(raw, ":"); idx >= 0 {
		raw = strings.TrimSpace(raw[idx+1:])
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func (s *Store) UpdateArticleRoot(projectID, roomID, messageID, title, text string, editorIDs ...string) (*Message, error) {
	title = strings.TrimSpace(title)
	text = strings.TrimSpace(text)
	if title == "" {
		return nil, fmt.Errorf("missing title")
	}
	if text == "" {
		return nil, fmt.Errorf("missing text")
	}

	allowed := map[string]bool{}
	for _, id := range editorIDs {
		if key := normalizeAuthorIdentity(id); key != "" {
			allowed[key] = true
		}
	}
	if len(allowed) == 0 {
		return nil, ErrForbidden
	}

	_ = s.EnsureRoomLoaded(projectID, roomID)

	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.Lock()
	var updated *Message
	for i := range r.Messages {
		msg := &r.Messages[i]
		if msg.ID != messageID {
			continue
		}
		if strings.TrimSpace(msg.ReplyToMessageID) != "" {
			r.mu.Unlock()
			return nil, fmt.Errorf("only root article can be edited")
		}
		if !allowed[normalizeAuthorIdentity(msg.AgentID)] {
			r.mu.Unlock()
			return nil, ErrForbidden
		}
		if !msg.TS.IsZero() && time.Since(msg.TS) > 12*time.Hour {
			r.mu.Unlock()
			return nil, fmt.Errorf("edit window expired")
		}
		msg.Title = title
		msg.Text = text
		updatedCopy := *msg
		updated = &updatedCopy
		articleID := strings.TrimSpace(msg.ArticleID)
		if articleID != "" {
			for j := range r.Messages {
				if strings.TrimSpace(r.Messages[j].ArticleID) == articleID {
					r.Messages[j].Title = title
				}
			}
		}
		break
	}
	if updated == nil {
		r.mu.Unlock()
		return nil, ErrMessageNotFound
	}

	r.cachedSimpleArticles = nil
	r.sigAcc = 0
	for _, m := range r.Messages {
		r.sigAcc ^= computeMessageSig(m)
	}
	r.touchLocked(time.Now())
	messagesSnapshot := append([]Message(nil), r.Messages...)
	r.mu.Unlock()

	authorName := ""
	s.mu.RLock()
	if updated != nil {
		authorName = s.resolveAuthorNameLocked(updated.AgentID)
	}
	s.mu.RUnlock()

	if s.persistToDisk {
		if err := s.rewriteRoomMessagesToDisk(projectID, roomID, messagesSnapshot); err != nil {
			return nil, err
		}
	}
	if s.pg != nil && updated != nil {
		_ = s.pg.UpdateArticleRoot(projectID, roomID, messageID, title, text, authorName, authorName)
	}
	s.notifyRoomListeners(projectID, roomID)
	return updated, nil
}

func (s *Store) rewriteRoomMessagesToDisk(projectID, roomID string, messages []Message) error {
	path := s.roomMessagesPath(projectID, roomID)
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, msg := range messages {
		b, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetPresence(projectID, roomID, agentID, status string, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[projectID]
	if !ok {
		return ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		return ErrRoomNotFound
	}
	if r.Presence == nil {
		r.Presence = make(map[string]Presence)
	}
	previous, existed := r.Presence[agentID]
	r.Presence[agentID] = Presence{AgentID: agentID, Status: status, LastSeen: ts}
	if s.pg != nil {
		_ = s.pg.SavePresence(projectID, roomID, agentID, status, ts)
	}
	if s.persistToDisk {
		if err := s.saveRoomMetaLocked(projectID, r); err != nil {
			if existed {
				r.Presence[agentID] = previous
			} else {
				delete(r.Presence, agentID)
			}
			return err
		}
	}
	return nil
}

func (s *Store) ListMessages(projectID, roomID string, limit int) ([]Message, error) {
	page, err := s.ListMessagesPage(projectID, roomID, MessagePageOptions{Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

func (s *Store) ListMessagesPage(projectID, roomID string, opts MessagePageOptions) (MessagePage, error) {
	if s == nil {
		return MessagePage{}, fmt.Errorf("store not available")
	}
	_ = s.EnsureRoomLoaded(projectID, roomID)

	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return MessagePage{}, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return MessagePage{}, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	r.touchLocked(time.Now())

	msgs := r.Messages
	visibleEnd := len(msgs)
	if opts.BeforeID != "" {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].ID == opts.BeforeID {
				visibleEnd = i
				break
			}
		}
	}
	if !opts.BeforeTS.IsZero() {
		for i := len(msgs) - 1; i >= 0; i-- {
			if !msgs[i].TS.IsZero() && !msgs[i].TS.Before(opts.BeforeTS) {
				visibleEnd = i
			}
		}
	}
	if visibleEnd < 0 {
		visibleEnd = 0
	}
	visible := msgs[:visibleEnd]
	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	start := 0
	if limit < len(visible) {
		start = len(visible) - limit
	}
	out := make([]Message, len(visible[start:]))
	copy(out, visible[start:])

	page := MessagePage{
		Messages: out,
		HasMore:  start > 0,
	}
	if page.HasMore && len(out) > 0 {
		page.NextBeforeID = out[0].ID
		if !out[0].TS.IsZero() {
			page.NextBeforeTS = out[0].TS.Format(time.RFC3339Nano)
		}
	}
	return page, nil
}

func (s *Store) ListPresence(projectID, roomID string) ([]Presence, error) {
	if s == nil {
		return nil, fmt.Errorf("store not available")
	}
	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Presence, 0, len(r.Presence))
	for _, pr := range r.Presence {
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (s *Store) ListArticles(projectID, roomID string, opts ArticleRangeOptions) ([]ArticleSummary, error) {
	if s == nil {
		return nil, fmt.Errorf("store not available")
	}
	_ = s.EnsureRoomLoaded(projectID, roomID)

	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.touchLocked(time.Now())

	timeField := strings.TrimSpace(strings.ToLower(opts.TimeField))
	if timeField == "" {
		timeField = "updated"
	}

	// 1. Check Lazy Sorted Articles Cache for simple, unconstrained pagination
	if opts.Simple && opts.FromTS.IsZero() && opts.ToTS.IsZero() && timeField == "updated" && r.cachedSimpleArticles != nil {
		list := r.cachedSimpleArticles
		limit := opts.Limit
		if opts.Last > 0 {
			limit = opts.Last
		} else if opts.Last < 0 {
			limit = len(list)
		}
		if limit <= 0 {
			limit = 200
		}
		if opts.Last >= 0 && limit > 2000 {
			limit = 2000
		}
		if len(list) > limit {
			list = list[:limit]
		}
		out := make([]ArticleSummary, len(list))
		copy(out, list)
		return out, nil
	}

	articles := map[string]*articleAccumulator{}
	order := make([]string, 0)
	for _, msg := range r.Messages {
		articleID := strings.TrimSpace(msg.ArticleID)
		if articleID == "" {
			articleID = strings.TrimSpace(msg.ID)
		}
		acc, exists := articles[articleID]
		if !exists {
			acc = &articleAccumulator{
				ProjectID:     msg.ProjectID,
				RoomID:        msg.RoomID,
				Board:         msg.RoomID,
				ArticleID:     articleID,
				Title:         strings.TrimSpace(msg.Title),
				Author:        s.resolveAuthorNameLocked(msg.AgentID),
				RootMessageID: strings.TrimSpace(msg.ID),
				StartedAt:     msg.TS,
				UpdatedAt:     msg.TS,
				ReplyCount:    0,
			}
			if acc.Title == "" {
				acc.Title = "(未命名文章)"
			}
			articles[articleID] = acc
			order = append(order, articleID)
			continue
		}
		if acc.Title == "" && strings.TrimSpace(msg.Title) != "" {
			acc.Title = strings.TrimSpace(msg.Title)
		}
		if !msg.TS.IsZero() && (acc.UpdatedAt.IsZero() || msg.TS.After(acc.UpdatedAt)) {
			acc.UpdatedAt = msg.TS
		}
		acc.ReplyCount++
	}

	list := make([]ArticleSummary, 0, len(order))
	for _, articleID := range order {
		acc := articles[articleID]
		if acc == nil {
			continue
		}
		matchAt := acc.UpdatedAt
		if timeField == "started" {
			matchAt = acc.StartedAt
		}
		if opts.Last <= 0 {
			if !opts.FromTS.IsZero() && matchAt.Before(opts.FromTS) {
				continue
			}
			if !opts.ToTS.IsZero() && !matchAt.Before(opts.ToTS) {
				continue
			}
		}
		summary := ArticleSummary{
			ProjectID:     acc.ProjectID,
			RoomID:        acc.RoomID,
			Board:         acc.Board,
			ArticleID:     acc.ArticleID,
			Article:       acc.ArticleID,
			Title:         acc.Title,
			Author:        acc.Author,
			RootMessageID: acc.RootMessageID,
			RootMessage:   acc.RootMessageID,
			StartedTS:     acc.StartedAt.Format(time.RFC3339Nano),
			UpdatedTS:     acc.UpdatedAt.Format(time.RFC3339Nano),
			ReplyCount:    acc.ReplyCount,
		}
		if !opts.Simple {
			replies := make([]Message, 0)
			for _, msg := range r.Messages {
				if strings.TrimSpace(msg.ArticleID) != acc.ArticleID {
					continue
				}
				if strings.TrimSpace(msg.ID) == acc.RootMessageID {
					summary.Body = msg.Text
					continue
				}
				msgCopy := msg
				s.normalizeMessageAliases(&msgCopy)
				replies = append(replies, msgCopy)
			}
			if len(replies) > 0 {
				summary.Replies = replies
			}
		}
		list = append(list, summary)
	}

	sort.Slice(list, func(i, j int) bool {
		if timeField == "started" {
			return list[i].StartedTS > list[j].StartedTS
		}
		return list[i].UpdatedTS > list[j].UpdatedTS
	})

	// Cache full simple list if no date filters and updated time field
	if opts.Simple && opts.FromTS.IsZero() && opts.ToTS.IsZero() && timeField == "updated" {
		r.cachedSimpleArticles = make([]ArticleSummary, len(list))
		copy(r.cachedSimpleArticles, list)
	}

	limit := opts.Limit
	if opts.Last > 0 {
		limit = opts.Last
	} else if opts.Last < 0 {
		limit = len(list)
	}
	if limit <= 0 {
		limit = 200
	}
	if opts.Last >= 0 && limit > 2000 {
		limit = 2000
	}
	if len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (s *Store) GetArticle(projectID, roomID, articleID string) (*ArticleSummary, error) {
	if s == nil {
		return nil, fmt.Errorf("store not available")
	}
	target := strings.TrimSpace(articleID)
	if target == "" {
		return nil, ErrMessageNotFound
	}
	_ = s.EnsureRoomLoaded(projectID, roomID)

	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	r.touchLocked(time.Now())

	var rootMsg *Message
	replies := make([]Message, 0)

	for i := range r.Messages {
		msg := &r.Messages[i]
		msgArt := strings.TrimSpace(msg.ArticleID)
		if msgArt == "" {
			msgArt = strings.TrimSpace(msg.ID)
		}
		if msgArt != target && strings.TrimSpace(msg.ID) != target {
			continue
		}

		if rootMsg == nil && (strings.TrimSpace(msg.ReplyToMessageID) == "" || strings.TrimSpace(msg.ID) == target) {
			rootCopy := *msg
			s.normalizeMessageAliases(&rootCopy)
			rootMsg = &rootCopy
		} else {
			replyCopy := *msg
			s.normalizeMessageAliases(&replyCopy)
			replies = append(replies, replyCopy)
		}
	}

	if rootMsg == nil {
		return nil, ErrMessageNotFound
	}

	startedTS := rootMsg.TS
	updatedTS := rootMsg.TS
	if len(replies) > 0 {
		lastReply := replies[len(replies)-1]
		if lastReply.TS.After(updatedTS) {
			updatedTS = lastReply.TS
		}
	}

	title := strings.TrimSpace(rootMsg.Title)
	if title == "" {
		title = "(未命名文章)"
	}

	summary := &ArticleSummary{
		ProjectID:     rootMsg.ProjectID,
		RoomID:        rootMsg.RoomID,
		Board:         rootMsg.RoomID,
		ArticleID:     target,
		Article:       target,
		Title:         title,
		Author:        s.resolveAuthorNameLocked(rootMsg.AgentID),
		Body:          rootMsg.Text,
		RootMessageID: rootMsg.ID,
		RootMessage:   rootMsg.ID,
		StartedTS:     startedTS.Format(time.RFC3339Nano),
		UpdatedTS:     updatedTS.Format(time.RFC3339Nano),
		ReplyCount:    len(replies),
		Replies:       replies,
	}
	return summary, nil
}

func (s *Store) SearchRoomsForClient(clientID, query string, now time.Time, limit int) []RoomInfo {
	query = strings.TrimSpace(strings.ToLower(query))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rooms := s.ListAllRoomsForClient(clientID, now)
	if query == "" {
		if len(rooms) > limit {
			return rooms[:limit]
		}
		return rooms
	}

	out := make([]RoomInfo, 0, len(rooms))
	for _, room := range rooms {
		haystack := strings.ToLower(strings.Join([]string{
			room.ProjectID,
			room.RoomID,
			room.Name,
			room.Category,
			room.Description,
			room.Owner,
		}, " "))
		if strings.Contains(haystack, query) {
			out = append(out, room)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (s *Store) SearchMessagesForClient(clientID, query string, limit int) []MessageSearchHit {
	query = strings.TrimSpace(strings.ToLower(query))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if query == "" {
		return []MessageSearchHit{}
	}

	s.mu.RLock()
	type searchRoomRef struct {
		pid  string
		rid  string
		name string
		r    *Room
	}
	var targets []searchRoomRef
	for projectID, project := range s.projects {
		for roomID, room := range project.Rooms {
			if !s.canClientAccessRoomLocked(clientID, roomAccessKey(projectID, roomID)) {
				continue
			}
			targets = append(targets, searchRoomRef{
				pid:  projectID,
				rid:  roomID,
				name: room.Name,
				r:    room,
			})
		}
	}
	s.mu.RUnlock()

	out := make([]MessageSearchHit, 0, limit)
	for _, t := range targets {
		t.r.mu.RLock()
		for i := len(t.r.Messages) - 1; i >= 0; i-- {
			msg := t.r.Messages[i]
			haystack := strings.ToLower(strings.Join([]string{
				msg.AgentID,
				msg.ArticleID,
				msg.Title,
				msg.Text,
			}, " "))
			if strings.Contains(haystack, query) {
				out = append(out, MessageSearchHit{
					ProjectID: t.pid,
					RoomID:    t.rid,
					Board:     t.rid,
					RoomName:  t.name,
					BoardName: t.name,
					Message:   msg,
				})
				if len(out) >= limit {
					t.r.mu.RUnlock()
					return out
				}
			}
		}
		t.r.mu.RUnlock()
	}
	return out
}

func (s *Store) ListArticlesByAuthorForClient(clientID, authorID string, limit int) []ArticleSummary {
	authorKey := normalizeAuthorIdentity(authorID)
	if authorKey == "" {
		return []ArticleSummary{}
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}

	out := make([]ArticleSummary, 0, limit)
	for _, snapshot := range s.historySnapshotsForClient(clientID) {
		articles := s.buildArticleSummariesFromMessages(snapshot.ProjectID, snapshot.RoomID, snapshot.Messages, true)
		for _, article := range articles {
			if normalizeAuthorIdentity(article.Author) != authorKey {
				continue
			}
			out = append(out, article)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedTS > out[j].StartedTS
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) ListRepliesByAuthorForClient(clientID, authorID string, limit int) []MessageSearchHit {
	authorKey := normalizeAuthorIdentity(authorID)
	if authorKey == "" {
		return []MessageSearchHit{}
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}

	out := make([]MessageSearchHit, 0, limit)
	for _, snapshot := range s.historySnapshotsForClient(clientID) {
		for _, msg := range snapshot.Messages {
			if strings.TrimSpace(msg.ReplyToMessageID) == "" && strings.TrimSpace(msg.ReplyToMessage) == "" {
				continue
			}
			if normalizeAuthorIdentity(msg.AgentID) != authorKey {
				continue
			}
			out = append(out, MessageSearchHit{
				ProjectID: snapshot.ProjectID,
				RoomID:    snapshot.RoomID,
				Board:     snapshot.RoomID,
				RoomName:  snapshot.RoomName,
				BoardName: snapshot.RoomName,
				Message:   msg,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Message.TS.After(out[j].Message.TS)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) historySnapshotsForClient(clientID string) []roomHistorySnapshot {
	type roomMeta struct {
		ProjectID string
		RoomID    string
		RoomName  string
		Path      string
		InMemory  []Message
	}

	s.mu.RLock()
	type tempRef struct {
		pid  string
		rid  string
		name string
		r    *Room
	}
	var refs []tempRef
	for projectID, project := range s.projects {
		for roomID, room := range project.Rooms {
			if !s.canClientAccessRoomLocked(clientID, roomAccessKey(projectID, roomID)) {
				continue
			}
			refs = append(refs, tempRef{
				pid:  projectID,
				rid:  roomID,
				name: room.Name,
				r:    room,
			})
		}
	}
	s.mu.RUnlock()

	rooms := make([]roomMeta, 0, len(refs))
	for _, item := range refs {
		item.r.mu.RLock()
		inMemory := make([]Message, len(item.r.Messages))
		copy(inMemory, item.r.Messages)
		item.r.mu.RUnlock()

		rooms = append(rooms, roomMeta{
			ProjectID: item.pid,
			RoomID:    item.rid,
			RoomName:  item.name,
			Path:      s.roomMessagesPath(item.pid, item.rid),
			InMemory:  inMemory,
		})
	}

	out := make([]roomHistorySnapshot, 0, len(rooms))
	for _, room := range rooms {
		messages := s.readRoomMessagesHistory(room.ProjectID, room.RoomID, room.Path)
		if len(messages) == 0 {
			messages = room.InMemory
		}
		out = append(out, roomHistorySnapshot{
			ProjectID: room.ProjectID,
			RoomID:    room.RoomID,
			RoomName:  room.RoomName,
			Messages:  messages,
		})
	}
	return out
}

func (s *Store) readRoomMessagesHistory(projectID, roomID, path string) []Message {
	if s.pg != nil {
		msgs, err := s.pg.LoadMessagesForRoom(projectID, roomID, 2000)
		if err == nil && len(msgs) > 0 {
			return msgs
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := make([]Message, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if strings.TrimSpace(msg.ProjectID) == "" {
			msg.ProjectID = projectID
		}
		if strings.TrimSpace(msg.RoomID) == "" {
			msg.RoomID = roomID
		}
		if strings.TrimSpace(msg.ProjectID) != projectID || strings.TrimSpace(msg.RoomID) != roomID {
			continue
		}
		if strings.TrimSpace(msg.ArticleID) == "" && strings.TrimSpace(msg.Article) != "" {
			msg.ArticleID = strings.TrimSpace(msg.Article)
		}
		if strings.TrimSpace(msg.ReplyToMessageID) == "" && strings.TrimSpace(msg.ReplyToMessage) != "" {
			msg.ReplyToMessageID = strings.TrimSpace(msg.ReplyToMessage)
		}
		if strings.TrimSpace(msg.ArticleID) == "" {
			if strings.TrimSpace(msg.ReplyToMessageID) != "" {
				for i := len(out) - 1; i >= 0; i-- {
					parent := out[i]
					if strings.TrimSpace(parent.ID) != strings.TrimSpace(msg.ReplyToMessageID) {
						continue
					}
					msg.ArticleID = firstNonEmpty(parent.ArticleID, parent.Article, parent.ID)
					if strings.TrimSpace(msg.Title) == "" {
						msg.Title = parent.Title
					}
					break
				}
			}
			if strings.TrimSpace(msg.ArticleID) == "" {
				msg.ArticleID = strings.TrimSpace(msg.ID)
			}
		}
		s.normalizeMessageAliases(&msg)
		out = append(out, msg)
	}
	return out
}

func (s *Store) buildArticleSummariesFromMessages(projectID, roomID string, messages []Message, simple bool) []ArticleSummary {
	articles := map[string]*articleAccumulator{}
	order := make([]string, 0)
	for _, msg := range messages {
		articleID := strings.TrimSpace(msg.ArticleID)
		if articleID == "" {
			articleID = strings.TrimSpace(msg.Article)
		}
		if articleID == "" {
			articleID = strings.TrimSpace(msg.ID)
		}
		acc, exists := articles[articleID]
		if !exists {
			authorName := strings.TrimSpace(msg.AgentID)
			if s != nil {
				authorName = s.resolveAuthorNameLocked(msg.AgentID)
			}
			acc = &articleAccumulator{
				ProjectID:     firstNonEmpty(msg.ProjectID, projectID),
				RoomID:        firstNonEmpty(msg.RoomID, roomID),
				Board:         firstNonEmpty(msg.Board, msg.RoomID, roomID),
				ArticleID:     articleID,
				Title:         strings.TrimSpace(msg.Title),
				Author:        authorName,
				RootMessageID: strings.TrimSpace(msg.ID),
				StartedAt:     msg.TS,
				UpdatedAt:     msg.TS,
				ReplyCount:    0,
			}
			if acc.Title == "" {
				acc.Title = "(未命名文章)"
			}
			articles[articleID] = acc
			order = append(order, articleID)
			continue
		}
		if acc.Title == "" && strings.TrimSpace(msg.Title) != "" {
			acc.Title = strings.TrimSpace(msg.Title)
		}
		if !msg.TS.IsZero() && (acc.UpdatedAt.IsZero() || msg.TS.After(acc.UpdatedAt)) {
			acc.UpdatedAt = msg.TS
		}
		acc.ReplyCount++
	}

	out := make([]ArticleSummary, 0, len(order))
	for _, articleID := range order {
		acc := articles[articleID]
		if acc == nil {
			continue
		}
		summary := ArticleSummary{
			ProjectID:     acc.ProjectID,
			RoomID:        acc.RoomID,
			Board:         acc.Board,
			ArticleID:     acc.ArticleID,
			Article:       acc.ArticleID,
			Title:         acc.Title,
			Author:        acc.Author,
			RootMessageID: acc.RootMessageID,
			RootMessage:   acc.RootMessageID,
			StartedTS:     acc.StartedAt.Format(time.RFC3339Nano),
			UpdatedTS:     acc.UpdatedAt.Format(time.RFC3339Nano),
			ReplyCount:    acc.ReplyCount,
		}
		if !simple {
			replies := make([]Message, 0)
			for _, msg := range messages {
				if strings.TrimSpace(firstNonEmpty(msg.ArticleID, msg.Article)) != acc.ArticleID {
					continue
				}
				if strings.TrimSpace(msg.ID) == acc.RootMessageID {
					summary.Body = msg.Text
					continue
				}
				replies = append(replies, msg)
			}
			if len(replies) > 0 {
				summary.Replies = replies
			}
		}
		out = append(out, summary)
	}
	return out
}

func (s *Store) ListMessagesAfter(projectID, roomID, afterID string, afterTS time.Time, limit int) ([]Message, error) {
	if s == nil {
		return nil, fmt.Errorf("store not available")
	}
	_ = s.EnsureRoomLoaded(projectID, roomID)

	s.mu.RLock()
	p, ok := s.projects[projectID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrProjectNotFound
	}
	r, ok := p.Rooms[roomID]
	if !ok {
		s.mu.RUnlock()
		return nil, ErrRoomNotFound
	}
	s.mu.RUnlock()

	r.mu.RLock()
	defer r.mu.RUnlock()
	r.touchLocked(time.Now())

	start := 0
	if strings.TrimSpace(afterID) != "" {
		found := false
		for i := range r.Messages {
			if r.Messages[i].ID == strings.TrimSpace(afterID) {
				start = i + 1
				found = true
				break
			}
		}
		if !found && afterTS.IsZero() {
			return nil, fmt.Errorf("cursor not found; provide after_ts to resume")
		}
	}
	out := make([]Message, 0)
	for i := start; i < len(r.Messages); i++ {
		m := r.Messages[i]
		if !afterTS.IsZero() && !m.TS.After(afterTS) {
			continue
		}
		out = append(out, m)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
