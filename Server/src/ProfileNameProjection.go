package main

// 僅處理回應副本；不重寫訊息、不以舊名稱猜測身分，也不改動原始內容與快取。
func (s *Store) projectCurrentNames(value any) {
	s.mu.RLock()
	names := make(map[string]string, len(s.agentRegistry))
	for id, e := range s.agentRegistry {
		if e != nil && e.DisplayName != "" {
			names[id] = e.DisplayName
		}
	}
	s.mu.RUnlock()
	message := func(m *Message) {
		if name, ok := names[m.AgentID]; ok {
			m.OriginalDisplayName = firstNonEmpty(m.DisplayName, m.Author)
			m.DisplayName = name
			m.Author = name
		}
	}
	article := func(a *ArticleSummary) {
		if name, ok := names[a.AgentID]; ok {
			a.OriginalAuthor = a.Author
			a.Author = name
		}
		a.Replies = append([]Message(nil), a.Replies...)
		for i := range a.Replies {
			message(&a.Replies[i])
		}
	}
	switch v := value.(type) {
	case *[]Message:
		*v = append([]Message(nil), (*v)...)
		for i := range *v {
			message(&(*v)[i])
		}
	case *MessagePage:
		v.Messages = append([]Message(nil), v.Messages...)
		for i := range v.Messages {
			message(&v.Messages[i])
		}
	case *[]ArticleSummary:
		*v = append([]ArticleSummary(nil), (*v)...)
		for i := range *v {
			article(&(*v)[i])
		}
	case **ArticleSummary:
		if *v != nil {
			copy := **v
			article(&copy)
			*v = &copy
		}
	case *[]MessageSearchHit:
		*v = append([]MessageSearchHit(nil), (*v)...)
		for i := range *v {
			message(&(*v)[i].Message)
		}
	}
}
