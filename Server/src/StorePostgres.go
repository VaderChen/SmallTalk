package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var validIdentRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

type PostgresStore struct {
	db            *sql.DB
	dsn           string
	ensureTableMu sync.Mutex
	knownTables   map[string]bool
}

func ConnectLocalPostgres() (*PostgresStore, error) {
	var candidates []string
	if env := strings.TrimSpace(os.Getenv("DATABASE_URL")); env != "" {
		candidates = append(candidates, env)
	}
	if env := strings.TrimSpace(os.Getenv("POSTGRES_DSN")); env != "" {
		candidates = append(candidates, env)
	}

	candidates = append(candidates,
		"postgres://postgres@127.0.0.1:5432/smalltalk?sslmode=disable",
		"postgres://127.0.0.1:5432/smalltalk?sslmode=disable",
		"postgres://localhost:5432/smalltalk?sslmode=disable",
		"host=localhost port=5432 dbname=smalltalk sslmode=disable",
		"host=127.0.0.1 port=5432 dbname=smalltalk sslmode=disable",
	)

	var lastErr error
	for _, dsn := range candidates {
		pg, err := NewPostgresStore(dsn)
		if err == nil {
			return pg, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("could not connect to local postgres: %w", lastErr)
}

func NewPostgresStore(dsn string) (*PostgresStore, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("empty postgres dsn")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres: %w", err)
	}

	db.SetMaxOpenConns(50)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping postgres: %w", err)
	}

	pg := &PostgresStore{
		db:          db,
		dsn:         dsn,
		knownTables: make(map[string]bool),
	}

	if err := pg.initSystemSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to init system schema: %w", err)
	}

	return pg, nil
}

func (pg *PostgresStore) Close() error {
	if pg != nil && pg.db != nil {
		return pg.db.Close()
	}
	return nil
}

func (pg *PostgresStore) SanitizeTableName(projectID, roomID string) string {
	projectID = strings.TrimSpace(strings.ToLower(projectID))
	roomID = strings.TrimSpace(strings.ToLower(roomID))
	if projectID == "" {
		projectID = "default"
	}
	if roomID == "" {
		roomID = "lobby"
	}

	sanitizedRoom := validIdentRe.ReplaceAllString(roomID, "_")
	for strings.Contains(sanitizedRoom, "__") {
		sanitizedRoom = strings.ReplaceAll(sanitizedRoom, "__", "_")
	}
	sanitizedRoom = strings.Trim(sanitizedRoom, "_")
	if sanitizedRoom == "" {
		sanitizedRoom = "unknown"
	}

	if projectID == "default" {
		return "board_" + sanitizedRoom
	}

	sanitizedProj := validIdentRe.ReplaceAllString(projectID, "_")
	sanitizedProj = strings.Trim(sanitizedProj, "_")
	return fmt.Sprintf("board_p_%s_%s", sanitizedProj, sanitizedRoom)
}

func (pg *PostgresStore) initSystemSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS boards (
		project_id VARCHAR(128) NOT NULL,
		room_id VARCHAR(128) NOT NULL,
		name TEXT NOT NULL,
		category VARCHAR(128),
		description TEXT,
		owner VARCHAR(128),
		table_name VARCHAR(128) NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (project_id, room_id)
	);

	CREATE TABLE IF NOT EXISTS agent_registry (
		client_id VARCHAR(128) PRIMARY KEY,
		display_name TEXT,
		mac_address VARCHAR(64),
		approved BOOLEAN DEFAULT FALSE,
		approved_at TEXT,
		blocked BOOLEAN DEFAULT FALSE,
		blocked_at TEXT,
		token_issued BOOLEAN DEFAULT FALSE,
		token_issued_at TEXT,
		token_expires_at TEXT,
		token TEXT,
		registered_at TEXT,
		last_seen_at TEXT,
		read_only BOOLEAN DEFAULT FALSE,
		read_only_at TEXT,
		meta JSONB
	);

	CREATE TABLE IF NOT EXISTS room_acls (
		client_id VARCHAR(128) PRIMARY KEY,
		allow_rooms JSONB,
		deny_rooms JSONB,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS presence (
		project_id VARCHAR(128) NOT NULL,
		room_id VARCHAR(128) NOT NULL,
		agent_id VARCHAR(128) NOT NULL,
		status VARCHAR(64) NOT NULL,
		last_seen TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (project_id, room_id, agent_id)
	);

	CREATE TABLE IF NOT EXISTS auth_tokens (
		token TEXT PRIMARY KEY,
		client_id VARCHAR(128) NOT NULL,
		kind VARCHAR(64),
		source_ip VARCHAR(64),
		mac_address VARCHAR(64),
		issued_at TEXT,
		expires_at TEXT
	);

	CREATE TABLE IF NOT EXISTS system_config (
		key VARCHAR(64) PRIMARY KEY,
		value JSONB NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`
	_, err := pg.db.Exec(schema)
	if err != nil {
		return err
	}
	_, _ = pg.db.Exec(`ALTER TABLE agent_registry ADD COLUMN IF NOT EXISTS read_only BOOLEAN DEFAULT FALSE;`)
	_, _ = pg.db.Exec(`ALTER TABLE agent_registry ADD COLUMN IF NOT EXISTS read_only_at TEXT;`)
	_, _ = pg.db.Exec(`ALTER TABLE presence ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ DEFAULT NOW();`)
	_, _ = pg.db.Exec(`ALTER TABLE presence ADD COLUMN IF NOT EXISTS last_seen_at TEXT;`)
	_, _ = pg.db.Exec(`ALTER TABLE presence ADD COLUMN IF NOT EXISTS display_name VARCHAR(256);`)
	_, _ = pg.db.Exec(`ALTER TABLE presence ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();`)
	return nil
}

func (pg *PostgresStore) EnsureBoardTable(projectID, roomID string) (string, error) {
	tableName := pg.SanitizeTableName(projectID, roomID)

	pg.ensureTableMu.Lock()
	defer pg.ensureTableMu.Unlock()

	if pg.knownTables[tableName] {
		return tableName, nil
	}

	query := fmt.Sprintf(`
	CREATE TABLE IF NOT EXISTS %s (
		id VARCHAR(128) PRIMARY KEY,
		project_id VARCHAR(128) NOT NULL,
		room_id VARCHAR(128) NOT NULL,
		board VARCHAR(128) NOT NULL,
		agent_id VARCHAR(128) NOT NULL,
		display_name TEXT,
		author TEXT,
		article_id VARCHAR(128),
		article VARCHAR(128),
		title TEXT,
		reply_to_message_id VARCHAR(128),
		reply_to_message VARCHAR(128),
		text TEXT NOT NULL,
		ts TIMESTAMPTZ NOT NULL,
		meta JSONB
	);
	CREATE INDEX IF NOT EXISTS idx_%s_ts ON %s (ts DESC);
	CREATE INDEX IF NOT EXISTS idx_%s_article ON %s (article_id);
	CREATE INDEX IF NOT EXISTS idx_%s_agent ON %s (agent_id);
	`, tableName, tableName, tableName, tableName, tableName, tableName, tableName)

	if _, err := pg.db.Exec(query); err != nil {
		return "", fmt.Errorf("failed to create board table %s: %w", tableName, err)
	}

	pg.knownTables[tableName] = true
	return tableName, nil
}

func (pg *PostgresStore) SaveBoardMetadata(projectID, roomID, name, category, description, owner string) error {
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return err
	}

	query := `
	INSERT INTO boards (project_id, room_id, name, category, description, owner, table_name, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	ON CONFLICT (project_id, room_id) DO UPDATE
	SET name = EXCLUDED.name,
	    category = EXCLUDED.category,
	    description = EXCLUDED.description,
	    owner = EXCLUDED.owner,
	    table_name = EXCLUDED.table_name,
	    updated_at = NOW();
	`
	_, err = pg.db.Exec(query, projectID, roomID, name, category, description, owner, tableName)
	return err
}

func (pg *PostgresStore) LoadAllBoards() ([]RoomInfo, error) {
	query := `
	SELECT project_id, room_id, name, category, description, owner, table_name
	FROM boards
	ORDER BY project_id ASC, room_id ASC;
	`
	rows, err := pg.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var boards []RoomInfo
	for rows.Next() {
		var b RoomInfo
		var tbl string
		if err := rows.Scan(&b.ProjectID, &b.RoomID, &b.Name, &b.Category, &b.Description, &b.Owner, &tbl); err != nil {
			return nil, err
		}
		b.Board = b.RoomID
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func (pg *PostgresStore) InsertMessage(m Message) error {
	tableName, err := pg.EnsureBoardTable(m.ProjectID, m.RoomID)
	if err != nil {
		return err
	}

	metaJSON, err := json.Marshal(m.Meta)
	if err != nil {
		return fmt.Errorf("encode message metadata: %w", err)
	}
	if len(metaJSON) == 0 {
		metaJSON = []byte("{}")
	}

	query := fmt.Sprintf(`
	INSERT INTO %s (
		id, project_id, room_id, board, agent_id, display_name, author,
		article_id, article, title, reply_to_message_id, reply_to_message,
		text, ts, meta
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7,
		$8, $9, $10, $11, $12,
		$13, $14, $15
	) ON CONFLICT (id) DO UPDATE
	SET title = EXCLUDED.title,
	    text = EXCLUDED.text,
	    display_name = EXCLUDED.display_name,
	    author = EXCLUDED.author,
	    meta = EXCLUDED.meta;
	`, tableName)

	_, err = pg.db.Exec(query,
		m.ID, m.ProjectID, m.RoomID, m.Board, m.AgentID, m.DisplayName, m.Author,
		m.ArticleID, m.Article, m.Title, m.ReplyToMessageID, m.ReplyToMessage,
		m.Text, m.TS, metaJSON,
	)
	return err
}

func (pg *PostgresStore) UpdateArticleRoot(projectID, roomID, messageID, title, text, displayName, author string) error {
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(`
	UPDATE %s
	SET title = $1, text = $2, display_name = $3, author = $4
	WHERE id = $5;
	`, tableName)

	res, err := pg.db.Exec(query, title, text, displayName, author, messageID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrMessageNotFound
	}
	return nil
}

func (pg *PostgresStore) UpdateMessageContentAndMeta(projectID, roomID, messageID, title, text string, meta map[string]any) error {
	if pg == nil || pg.db == nil {
		return nil
	}
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return err
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode message metadata: %w", err)
	}
	query := fmt.Sprintf(`
	UPDATE %s
	SET title = $1, text = $2, meta = $3
	WHERE id = $4;
	`, tableName)
	res, err := pg.db.Exec(query, title, text, metaBytes, messageID)
	if err != nil {
		return err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows == 0 {
		return ErrMessageNotFound
	}
	return nil
}

func (pg *PostgresStore) UpdateMessageMeta(projectID, roomID, messageID string, meta map[string]any) error {
	if pg == nil || pg.db == nil {
		return nil
	}
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return err
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode message metadata: %w", err)
	}
	query := fmt.Sprintf(`
	UPDATE %s
	SET meta = $1
	WHERE id = $2;
	`, tableName)
	res, err := pg.db.Exec(query, metaBytes, messageID)
	if err != nil {
		return err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows == 0 {
		return ErrMessageNotFound
	}
	return nil
}

// DeleteBoard removes both the catalog entry and all board-owned data in one
// transaction. Keeping the per-board table after deleting metadata would allow
// old messages to reappear when the same board ID is created again.
func (pg *PostgresStore) DeleteBoard(projectID, roomID string) error {
	if pg == nil || pg.db == nil {
		return nil
	}
	projectID = firstNonEmpty(projectID, "default")
	tableName := pg.SanitizeTableName(projectID, roomID)
	tx, err := pg.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM presence WHERE project_id = $1 AND room_id = $2`, projectID, roomID); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM boards WHERE project_id = $1 AND room_id = $2`, projectID, roomID); err != nil {
		return err
	}
	if _, err = tx.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, tableName)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	pg.ensureTableMu.Lock()
	delete(pg.knownTables, tableName)
	pg.ensureTableMu.Unlock()
	return nil
}

func (pg *PostgresStore) DeleteArticleMessages(projectID, roomID, articleID string) error {
	if pg == nil || pg.db == nil {
		return nil
	}
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return err
	}
	projectID = firstNonEmpty(projectID, "default")
	query := fmt.Sprintf(`DELETE FROM %s WHERE (project_id = $1 OR project_id = '' OR project_id = 'default') AND (room_id = $2 OR board = $2) AND (article_id = $3 OR id = $3)`, tableName)
	_, err = pg.db.Exec(query, projectID, roomID, articleID)
	return err
}

func (pg *PostgresStore) DeleteMessage(projectID, roomID, messageID string) error {
	if pg == nil || pg.db == nil {
		return nil
	}
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return err
	}
	projectID = firstNonEmpty(projectID, "default")
	query := fmt.Sprintf(`DELETE FROM %s WHERE id = $1 AND (project_id = $2 OR project_id = '' OR project_id = 'default') AND (room_id = $3 OR board = $3)`, tableName)
	res, err := pg.db.Exec(query, messageID, projectID, roomID)
	if err != nil {
		return err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows == 0 {
		return ErrMessageNotFound
	}
	return nil
}

func (pg *PostgresStore) DeleteMessagesOlderThan(projectID, roomID string, cutoff time.Time) (int64, error) {
	if pg == nil || pg.db == nil {
		return 0, nil
	}
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE ts < $1;`, tableName)
	res, err := pg.db.Exec(query, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (pg *PostgresStore) LoadMessagesForRoom(projectID, roomID string, limit int) ([]Message, error) {
	tableName, err := pg.EnsureBoardTable(projectID, roomID)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 2000
	}

	projectID = firstNonEmpty(projectID, "default")
	query := fmt.Sprintf(`
	SELECT id, project_id, room_id, board, agent_id, display_name, author,
	       article_id, article, title, reply_to_message_id, reply_to_message,
	       text, ts, meta
	FROM %s
	WHERE (project_id = $1 OR project_id = '' OR project_id = 'default') AND (room_id = $2 OR board = $2)
	ORDER BY ts ASC
	LIMIT $3;
	`, tableName)

	rows, err := pg.db.Query(query, projectID, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var metaBytes []byte
		var dispName, auth, artID, art, title, repID, rep sql.NullString
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.RoomID, &m.Board, &m.AgentID, &dispName, &auth,
			&artID, &art, &title, &repID, &rep,
			&m.Text, &m.TS, &metaBytes,
		); err != nil {
			return nil, err
		}
		m.DisplayName = dispName.String
		m.Author = auth.String
		m.ArticleID = artID.String
		m.Article = art.String
		m.Title = title.String
		m.ReplyToMessageID = repID.String
		m.ReplyToMessage = rep.String
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &m.Meta)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (pg *PostgresStore) SaveAgentRegistryEntry(entry *AgentRegistryEntry) error {
	if entry == nil || strings.TrimSpace(entry.ClientID) == "" {
		return nil
	}

	metaJSON, _ := json.Marshal(entry.Meta)
	if len(metaJSON) == 0 {
		metaJSON = []byte("{}")
	}

	query := `
	INSERT INTO agent_registry (
		client_id, display_name, mac_address, approved, approved_at,
		blocked, blocked_at, token_issued, token_issued_at, token_expires_at,
		token, registered_at, last_seen_at, read_only, read_only_at, meta
	) VALUES (
		$1, $2, $3, $4, $5,
		$6, $7, $8, $9, $10,
		$11, $12, $13, $14, $15, $16
	) ON CONFLICT (client_id) DO UPDATE
	SET display_name = EXCLUDED.display_name,
	    mac_address = EXCLUDED.mac_address,
	    approved = EXCLUDED.approved,
	    approved_at = EXCLUDED.approved_at,
	    blocked = EXCLUDED.blocked,
	    blocked_at = EXCLUDED.blocked_at,
	    token_issued = EXCLUDED.token_issued,
	    token_issued_at = EXCLUDED.token_issued_at,
	    token_expires_at = EXCLUDED.token_expires_at,
	    token = EXCLUDED.token,
	    registered_at = EXCLUDED.registered_at,
	    last_seen_at = EXCLUDED.last_seen_at,
	    read_only = EXCLUDED.read_only,
	    read_only_at = EXCLUDED.read_only_at,
	    meta = EXCLUDED.meta;
	`

	_, err := pg.db.Exec(query,
		entry.ClientID, entry.DisplayName, entry.MACAddress, entry.Approved, entry.ApprovedAt,
		entry.Blocked, entry.BlockedAt, entry.TokenIssued, entry.TokenIssuedAt, entry.TokenExpiresAt,
		entry.Token, entry.RegisteredAt, entry.LastSeenAt, entry.ReadOnly, entry.ReadOnlyAt, metaJSON,
	)
	return err
}

func (pg *PostgresStore) LoadAllAgentRegistry() (map[string]*AgentRegistryEntry, error) {
	query := `
	SELECT client_id, display_name, mac_address, approved, approved_at,
	       blocked, blocked_at, token_issued, token_issued_at, token_expires_at,
	       token, registered_at, last_seen_at, read_only, read_only_at, meta
	FROM agent_registry;
	`
	rows, err := pg.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*AgentRegistryEntry)
	for rows.Next() {
		var e AgentRegistryEntry
		var metaBytes []byte
		var disp, mac, appAt, blkAt, tokIssAt, tokExpAt, tok, regAt, lastSeen, roAt sql.NullString
		if err := rows.Scan(
			&e.ClientID, &disp, &mac, &e.Approved, &appAt,
			&e.Blocked, &blkAt, &e.TokenIssued, &tokIssAt, &tokExpAt,
			&tok, &regAt, &lastSeen, &e.ReadOnly, &roAt, &metaBytes,
		); err != nil {
			return nil, err
		}
		e.DisplayName = disp.String
		e.MACAddress = mac.String
		e.ApprovedAt = appAt.String
		e.BlockedAt = blkAt.String
		e.TokenIssuedAt = tokIssAt.String
		e.TokenExpiresAt = tokExpAt.String
		e.Token = tok.String
		e.RegisteredAt = regAt.String
		e.LastSeenAt = lastSeen.String
		e.ReadOnlyAt = roAt.String
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &e.Meta)
		}
		out[e.ClientID] = &e
	}
	return out, rows.Err()
}

func (pg *PostgresStore) SavePresence(projectID, roomID, agentID, status string, ts time.Time) error {
	query := `
	INSERT INTO presence (project_id, room_id, agent_id, status, last_seen, last_seen_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $5)
	ON CONFLICT (project_id, room_id, agent_id) DO UPDATE
	SET status = EXCLUDED.status,
	    last_seen = EXCLUDED.last_seen,
	    last_seen_at = EXCLUDED.last_seen_at,
	    updated_at = EXCLUDED.updated_at;
	`
	_, err := pg.db.Exec(query, projectID, roomID, agentID, status, ts, ts.Format(time.RFC3339Nano))
	return err
}

func (pg *PostgresStore) LoadAllPresence() (map[string]map[string]Presence, error) {
	query := `
	SELECT project_id, room_id, agent_id, COALESCE(status, ''), 
	       COALESCE(last_seen, updated_at, NOW()) 
	FROM presence;`
	rows, err := pg.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string]Presence)
	for rows.Next() {
		var pid, rid, aid, status string
		var lastSeen time.Time
		if err := rows.Scan(&pid, &rid, &aid, &status, &lastSeen); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s/%s", pid, rid)
		if out[key] == nil {
			out[key] = make(map[string]Presence)
		}
		out[key][aid] = Presence{AgentID: aid, Status: status, LastSeen: lastSeen}
	}
	return out, rows.Err()
}

func (pg *PostgresStore) SaveRoomACL(clientID string, allowRooms, denyRooms map[string]bool) error {
	var allowList, denyList []string
	for k, v := range allowRooms {
		if v {
			allowList = append(allowList, k)
		}
	}
	for k, v := range denyRooms {
		if v {
			denyList = append(denyList, k)
		}
	}

	wJSON, _ := json.Marshal(allowList)
	bJSON, _ := json.Marshal(denyList)

	query := `
	INSERT INTO room_acls (client_id, allow_rooms, deny_rooms, updated_at)
	VALUES ($1, $2, $3, NOW())
	ON CONFLICT (client_id) DO UPDATE
	SET allow_rooms = EXCLUDED.allow_rooms,
	    deny_rooms = EXCLUDED.deny_rooms,
	    updated_at = NOW();
	`
	_, err := pg.db.Exec(query, clientID, wJSON, bJSON)
	return err
}

func (pg *PostgresStore) LoadAllRoomACLs() (map[string]*ClientRoomACL, error) {
	query := `SELECT client_id, allow_rooms, deny_rooms FROM room_acls;`
	rows, err := pg.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*ClientRoomACL)
	for rows.Next() {
		var cid string
		var wBytes, bBytes []byte
		if err := rows.Scan(&cid, &wBytes, &bBytes); err != nil {
			return nil, err
		}
		var allowList, denyList []string
		if len(wBytes) > 0 {
			_ = json.Unmarshal(wBytes, &allowList)
		}
		if len(bBytes) > 0 {
			_ = json.Unmarshal(bBytes, &denyList)
		}
		acl := &ClientRoomACL{
			Allow: make(map[string]bool),
			Deny:  make(map[string]bool),
		}
		for _, item := range allowList {
			acl.Allow[item] = true
		}
		for _, item := range denyList {
			acl.Deny[item] = true
		}
		out[cid] = acl
	}
	return out, rows.Err()
}

func (pg *PostgresStore) SaveAuthToken(record *AuthTokenRecord) error {
	if record == nil || strings.TrimSpace(record.Token) == "" {
		return nil
	}

	query := `
	INSERT INTO auth_tokens (
		token, client_id, kind, source_ip, mac_address, issued_at, expires_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7
	) ON CONFLICT (token) DO UPDATE
	SET client_id = EXCLUDED.client_id,
	    kind = EXCLUDED.kind,
	    source_ip = EXCLUDED.source_ip,
	    mac_address = EXCLUDED.mac_address,
	    issued_at = EXCLUDED.issued_at,
	    expires_at = EXCLUDED.expires_at;
	`
	_, err := pg.db.Exec(query,
		record.Token, record.ClientID, record.Kind,
		record.SourceIP, record.MACAddress, record.IssuedAt, record.ExpiresAt,
	)
	return err
}

func (pg *PostgresStore) LoadAllAuthTokens() (map[string]*AuthTokenRecord, error) {
	query := `
	SELECT token, client_id, kind, source_ip, mac_address, issued_at, expires_at
	FROM auth_tokens;
	`
	rows, err := pg.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*AuthTokenRecord)
	for rows.Next() {
		var r AuthTokenRecord
		var src, mac, iss, exp sql.NullString
		if err := rows.Scan(
			&r.Token, &r.ClientID, &r.Kind, &src, &mac, &iss, &exp,
		); err != nil {
			return nil, err
		}
		r.SourceIP = src.String
		r.MACAddress = mac.String
		r.IssuedAt = iss.String
		r.ExpiresAt = exp.String
		out[r.Token] = &r
	}
	return out, rows.Err()
}

func (pg *PostgresStore) DeleteAgentRegistry(clientID string) error {
	query := `DELETE FROM agent_registry WHERE client_id = $1;`
	_, err := pg.db.Exec(query, clientID)
	return err
}

func (pg *PostgresStore) DeleteRoomACL(clientID string) error {
	query := `DELETE FROM room_acls WHERE client_id = $1;`
	_, err := pg.db.Exec(query, clientID)
	return err
}

func (pg *PostgresStore) DeleteAuthTokensForClient(clientID string) error {
	query := `DELETE FROM auth_tokens WHERE client_id = $1;`
	_, err := pg.db.Exec(query, clientID)
	return err
}

func (pg *PostgresStore) DeleteAuthToken(token string) error {
	query := `DELETE FROM auth_tokens WHERE token = $1;`
	_, err := pg.db.Exec(query, token)
	return err
}

func (pg *PostgresStore) DeleteAuthTokensForClientKind(clientID, kind string) error {
	query := `DELETE FROM auth_tokens WHERE client_id = $1 AND kind = $2;`
	_, err := pg.db.Exec(query, clientID, kind)
	return err
}

func (pg *PostgresStore) DeleteAgentData(clientID string) error {
	if pg == nil || pg.db == nil {
		return nil
	}
	tx, err := pg.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range []string{
		`DELETE FROM auth_tokens WHERE client_id = $1`,
		`DELETE FROM room_acls WHERE client_id = $1`,
		`DELETE FROM agent_registry WHERE client_id = $1`,
	} {
		if _, err := tx.Exec(query, clientID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (pg *PostgresStore) GetSystemConfig(key string) ([]byte, error) {
	query := `SELECT value FROM system_config WHERE key = $1;`
	var val []byte
	err := pg.db.QueryRow(query, key).Scan(&val)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return val, err
}

func (pg *PostgresStore) SetSystemConfig(key string, val any) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	query := `
	INSERT INTO system_config (key, value, updated_at)
	VALUES ($1, $2, NOW())
	ON CONFLICT (key) DO UPDATE
	SET value = EXCLUDED.value,
	    updated_at = NOW();
	`
	_, err = pg.db.Exec(query, key, b)
	return err
}

func (pg *PostgresStore) CountTodayMessages(todayStart time.Time) int {
	if pg == nil || pg.db == nil {
		return 0
	}
	boards, err := pg.LoadAllBoards()
	if err != nil {
		return 0
	}
	total := 0
	for _, b := range boards {
		tableName := pg.SanitizeTableName(b.ProjectID, b.RoomID)
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE ts >= $1;", tableName)
		if err := pg.db.QueryRow(query, todayStart).Scan(&count); err == nil {
			total += count
		}
	}
	return total
}
