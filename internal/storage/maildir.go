package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/emersion/go-maildir"
)

type Manager struct {
	baseDir string
	mu      sync.RWMutex
	dirs    map[string]*UserStorage
}

func New(baseDir string) *Manager {
	return &Manager{
		baseDir: baseDir,
		dirs:    make(map[string]*UserStorage),
	}
}

type UserStorage struct {
	dir      maildir.Dir
	username string
}

func (m *Manager) ForUser(username string) (*UserStorage, error) {
	m.mu.RLock()
	us, ok := m.dirs[username]
	m.mu.RUnlock()
	if ok {
		return us, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if us, ok := m.dirs[username]; ok {
		return us, nil
	}

	userDir := filepath.Join(m.baseDir, username)
	if err := os.MkdirAll(userDir, 0700); err != nil {
		return nil, fmt.Errorf("create maildir for %s: %w", username, err)
	}

	md := maildir.Dir(userDir)
	if err := md.Init(); err != nil {
		return nil, fmt.Errorf("init maildir for %s: %w", username, err)
	}

	us = &UserStorage{dir: md, username: username}
	m.dirs[username] = us
	return us, nil
}

func (us *UserStorage) Deliver(r io.Reader) (string, error) {
	del, err := maildir.NewDelivery(string(us.dir))
	if err != nil {
		return "", fmt.Errorf("create delivery: %w", err)
	}
	if _, err := io.Copy(del, r); err != nil {
		del.Abort()
		return "", fmt.Errorf("write message: %w", err)
	}
	if err := del.Close(); err != nil {
		return "", fmt.Errorf("close delivery: %w", err)
	}
	return "", nil
}

func (us *UserStorage) Dir() maildir.Dir {
	return us.dir
}

func (m *Manager) BaseDir() string {
	return m.baseDir
}
