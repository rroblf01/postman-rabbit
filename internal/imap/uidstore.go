package imap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// uidMu serializes all read-modify-write cycles on the per-mailbox UID list.
// Mail volume here is low, so a single global lock is simpler and safe.
var uidMu sync.Mutex

const uidListName = ".uidlist"

// baseKey strips the maildir info/flags suffix (":2,...") from a filename,
// yielding the stable identity used to track a message's UID across flag
// changes and new/ -> cur/ moves.
func baseKey(name string) string {
	if idx := strings.LastIndex(name, ":2,"); idx >= 0 {
		return name[:idx]
	}
	return name
}

// uidState is the persisted UID assignment for a single mailbox: a validity
// stamp, the next UID to hand out, and the current key->UID map.
type uidState struct {
	validity uint32
	next     uint32
	keys     map[string]uint32
}

// syncUIDs reconciles the on-disk UID list with the messages actually present
// in the mailbox's new/ and cur/ directories: it drops UIDs for deleted
// messages and assigns the next available UID to new ones, monotonically and
// stably (RFC 3501 requires UIDs to be ascending and never reused). The updated
// list is written back atomically.
func syncUIDs(dir string) uidState {
	uidMu.Lock()
	defer uidMu.Unlock()

	st := loadUIDState(dir)

	present := make(map[string]bool)
	for _, sub := range []string{"new", "cur"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			present[baseKey(e.Name())] = true
		}
	}

	changed := false

	for k := range st.keys {
		if !present[k] {
			delete(st.keys, k)
			changed = true
		}
	}

	// Assign UIDs to newly seen messages in a deterministic (sorted) order.
	var fresh []string
	for k := range present {
		if _, ok := st.keys[k]; !ok {
			fresh = append(fresh, k)
		}
	}
	sort.Strings(fresh)
	for _, k := range fresh {
		st.keys[k] = st.next
		st.next++
		changed = true
	}

	if changed {
		saveUIDState(dir, st)
	}
	return st
}

func loadUIDState(dir string) uidState {
	st := uidState{keys: make(map[string]uint32)}

	f, err := os.Open(filepath.Join(dir, uidListName))
	if err != nil {
		st.validity = uidVal(dir)
		st.next = 1
		return st
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if sc.Scan() {
		// Header line: "<validity> <next>"
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 {
			if v, err := strconv.ParseUint(fields[0], 10, 32); err == nil {
				st.validity = uint32(v)
			}
			if n, err := strconv.ParseUint(fields[1], 10, 32); err == nil {
				st.next = uint32(n)
			}
		}
	}
	for sc.Scan() {
		line := sc.Text()
		sp := strings.IndexByte(line, ' ')
		if sp <= 0 {
			continue
		}
		uid, err := strconv.ParseUint(line[:sp], 10, 32)
		if err != nil {
			continue
		}
		st.keys[line[sp+1:]] = uint32(uid)
	}

	if st.validity == 0 {
		st.validity = uidVal(dir)
	}
	if st.next == 0 {
		st.next = 1
	}
	return st
}

func saveUIDState(dir string, st uidState) {
	tmp := filepath.Join(dir, uidListName+".tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return
	}

	w := bufio.NewWriter(f)
	fmt.Fprintf(w, "%d %d\n", st.validity, st.next)

	type kv struct {
		key string
		uid uint32
	}
	list := make([]kv, 0, len(st.keys))
	for k, u := range st.keys {
		list = append(list, kv{k, u})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].uid < list[j].uid })
	for _, e := range list {
		fmt.Fprintf(w, "%d %s\n", e.uid, e.key)
	}

	if w.Flush() != nil || f.Close() != nil {
		return
	}
	os.Rename(tmp, filepath.Join(dir, uidListName))
}
