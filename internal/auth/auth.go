package auth

import (
	"crypto/subtle"
	"strings"
	"sync"
)

type Manager struct {
	mu    sync.RWMutex
	users map[string]string
}

func New(users map[string]string) *Manager {
	cp := make(map[string]string, len(users))
	for k, v := range users {
		cp[strings.ToLower(k)] = v
	}
	return &Manager{users: cp}
}

func (m *Manager) Authenticate(username, password string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pass, ok := m.users[strings.ToLower(username)]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(pass)) == 1
}

func (m *Manager) Exists(username string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.users[strings.ToLower(username)]
	return ok
}

func (m *Manager) Users() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	us := make([]string, 0, len(m.users))
	for u := range m.users {
		us = append(us, u)
	}
	return us
}
