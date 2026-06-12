package imap

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-maildir"
	"github.com/rroblf01/postman-rabbit/internal/auth"
	"github.com/rroblf01/postman-rabbit/internal/storage"
)

const delim = '/'

func NewSession(authMgr *auth.Manager, store *storage.Manager) imapserver.Session {
	return &session{auth: authMgr, store: store}
}

type session struct {
	auth     *auth.Manager
	store    *storage.Manager
	username string
	userDir  string
}

func (s *session) Close() error {
	return nil
}

func (s *session) Login(username, password string) error {
	if !s.auth.Authenticate(username, password) {
		return imapserver.ErrAuthFailed
	}
	us, err := s.store.ForUser(username)
	if err != nil {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Text: "Cannot access user storage",
		}
	}
	s.username = username
	s.userDir = string(us.Dir())
	return nil
}

func (s *session) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	mboxDir := s.mboxPath(name)
	info, err := os.Stat(mboxDir)
	if err != nil {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeNonExistent,
			Text: "No such mailbox",
		}
	}
	if !info.IsDir() {
		return nil, &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Code: imap.ResponseCodeNonExistent,
			Text: "Not a mailbox",
		}
	}
	msgs := scanDir(mboxDir)
	unseen := 0
	for _, m := range msgs {
		if !hasFlag(m.flags, "S") {
			unseen++
		}
	}
	uidNext := uint32(len(msgs) + 1)
	return &imap.SelectData{
		UIDNext:           imap.UID(uidNext),
		UIDValidity:       uidVal(mboxDir),
		NumMessages:       uint32(len(msgs)),
		FirstUnseenSeqNum: uint32(unseen),
	}, nil
}

func (s *session) Unselect() error {
	return nil
}

func (s *session) Create(name string, options *imap.CreateOptions) error {
	mboxDir := s.mboxPath(name)
	if err := os.MkdirAll(mboxDir, 0700); err != nil {
		return &imap.Error{
			Type: imap.StatusResponseTypeNo,
			Text: "Cannot create mailbox",
		}
	}
	return maildir.Dir(mboxDir).Init()
}

func (s *session) Delete(name string) error {
	return os.RemoveAll(s.mboxPath(name))
}

func (s *session) Rename(oldName, newName string, options *imap.RenameOptions) error {
	return os.Rename(s.mboxPath(oldName), s.mboxPath(newName))
}

func (s *session) Subscribe(name string) error  { return nil }
func (s *session) Unsubscribe(name string) error { return nil }

func (s *session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	boxes := s.listMboxes()
	if len(patterns) == 0 {
		return w.WriteList(&imap.ListData{
			Attrs: []imap.MailboxAttr{imap.MailboxAttrNoSelect},
			Delim: delim,
		})
	}
	for _, box := range boxes {
		for _, pat := range patterns {
			if imapserver.MatchList(box, delim, ref, pat) {
				w.WriteList(&imap.ListData{
					Attrs:   []imap.MailboxAttr{imap.MailboxAttrUnmarked},
					Delim:   delim,
					Mailbox: box,
				})
				break
			}
		}
	}
	return nil
}

func (s *session) Status(name string, options *imap.StatusOptions) (*imap.StatusData, error) {
	mboxDir := s.mboxPath(name)
	msgs := scanDir(mboxDir)
	data := &imap.StatusData{Mailbox: name}
	if options == nil {
		return data, nil
	}
	if options.NumMessages {
		n := uint32(len(msgs))
		data.NumMessages = &n
	}
	if options.NumUnseen {
		unseen := uint32(0)
		for _, m := range msgs {
			if !hasFlag(m.flags, "S") {
				unseen++
			}
		}
		data.NumUnseen = &unseen
	}
	if options.UIDNext {
		data.UIDNext = imap.UID(len(msgs) + 1)
	}
	if options.UIDValidity {
		data.UIDValidity = uidVal(mboxDir)
	}
	return data, nil
}

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	mboxDir := s.mboxPath(mailbox)

	sub := filepath.Join(mboxDir, "new")
	before := make(map[string]bool)
	entries, _ := os.ReadDir(sub)
	for _, e := range entries {
		before[e.Name()] = true
	}

	del, err := maildir.NewDelivery(mboxDir)
	if err != nil {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Cannot append"}
	}
	if _, err := io.Copy(del, r); err != nil {
		del.Abort()
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Cannot read message"}
	}
	if err := del.Close(); err != nil {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Cannot save message"}
	}

	key := findNewKey(sub, before)
	uid := uidFromKey(key)

	if options != nil && len(options.Flags) > 0 {
		suffix := ":2," + strings.Join(maildirFlagsFromIMAP(options.Flags), "")
		oldPath := filepath.Join(sub, key)
		newPath := filepath.Join(mboxDir, "cur", key+suffix)
		os.Rename(oldPath, newPath)
	}

	return &imap.AppendData{
		UIDValidity: uidVal(mboxDir),
		UID:         imap.UID(uid),
	}, nil
}

func findNewKey(sub string, before map[string]bool) string {
	entries, _ := os.ReadDir(sub)
	for _, e := range entries {
		if !before[e.Name()] {
			return e.Name()
		}
	}
	return ""
}

func (s *session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return nil
}

func (s *session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	select {
	case <-stop:
		return nil
	case <-ticker.C:
		return nil
	}
}

func (s *session) Copy(numSet imap.NumSet, destName string) (*imap.CopyData, error) {
	destDir := s.mboxPath(destName)
	var destUIDs imap.UIDSet
	var srcUIDs imap.UIDSet
	forEach(numSet, s.userDir, func(m mboxMsg) {
		srcUIDs.AddNum(imap.UID(m.uid))
		if uid := copyMsg(m.path, destDir); uid > 0 {
			destUIDs.AddNum(imap.UID(uid))
		}
	})
	return &imap.CopyData{
		SourceUIDs:  srcUIDs,
		UIDValidity: uidVal(destDir),
		DestUIDs:    destUIDs,
	}, nil
}

func (s *session) Move(w *imapserver.MoveWriter, numSet imap.NumSet, destName string) error {
	destDir := s.mboxPath(destName)
	forEach(numSet, s.userDir, func(m mboxMsg) {
		if uid := copyMsg(m.path, destDir); uid > 0 {
			os.Remove(m.path)
		}
	})
	return nil
}

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	num := 1
	forEach(nil, s.userDir, func(m mboxMsg) {
		if hasFlag(m.flags, "T") {
			if uids == nil || uids.Contains(imap.UID(m.uid)) {
				os.Remove(m.path)
				w.WriteExpunge(uint32(num))
				num++
			}
		}
	})
	return nil
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	data := &imap.SearchData{}
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet
	forEach(nil, s.userDir, func(m mboxMsg) {
		switch kind {
		case imapserver.NumKindSeq:
			seqSet.AddNum(m.seq)
		case imapserver.NumKindUID:
			uidSet.AddNum(imap.UID(m.uid))
		}
	})
	switch kind {
	case imapserver.NumKindSeq:
		data.All = seqSet
	case imapserver.NumKindUID:
		data.All = uidSet
	}
	return data, nil
}

func (s *session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	forEach(numSet, s.userDir, func(m mboxMsg) {
		raw, err := os.ReadFile(m.path)
		if err != nil {
			return
		}
		fw := w.CreateMessage(m.seq)
		fw.WriteUID(imap.UID(m.uid))
		if options.Flags {
			fw.WriteFlags(imapFlagsFromMaildir(m.flags))
		}
		if options.InternalDate {
			fw.WriteInternalDate(m.date)
		}
		if options.RFC822Size {
			fw.WriteRFC822Size(m.size)
		}
		if options.Envelope {
			fw.WriteEnvelope(envelope(raw))
		}
		if options.BodyStructure != nil {
			fw.WriteBodyStructure(bodyStructure(raw))
		}
		for _, bs := range options.BodySection {
			var sectionData []byte
			switch bs.Specifier {
			case imap.PartSpecifierHeader:
				sectionData = extractHeader(raw)
			default:
				sectionData = raw
			}
			wc := fw.WriteBodySection(bs, int64(len(sectionData)))
			wc.Write(sectionData)
			wc.Close()
		}
		fw.Close()
	})
	return nil
}

func (s *session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, storeFlags *imap.StoreFlags, options *imap.StoreOptions) error {
	forEach(numSet, s.userDir, func(m mboxMsg) {
		newFlags := applyFlags(m.flags, storeFlags)
		updateFlags(m.path, newFlags)
	})
	return nil
}

func (s *session) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{
		Personal: []imap.NamespaceDescriptor{
			{Prefix: "", Delim: delim},
		},
	}, nil
}

func (s *session) mboxPath(name string) string {
	if name == "" || name == "INBOX" || name == "INBOX/" {
		return s.userDir
	}
	return filepath.Join(s.userDir, strings.TrimPrefix(name, "INBOX/"))
}

func (s *session) listMboxes() []string {
	var boxes []string
	filepath.WalkDir(s.userDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(s.userDir, path)
		if rel == "." {
			boxes = append(boxes, "INBOX")
			return nil
		}
		if rel == "new" || rel == "cur" || rel == "tmp" {
			return filepath.SkipDir
		}
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		boxes = append(boxes, "INBOX/"+rel)
		return nil
	})
	sort.Strings(boxes)
	return boxes
}

type mboxMsg struct {
	uid   uint32
	seq   uint32
	path  string
	flags []string
	date  time.Time
	size  int64
}

func scanDir(dir string) []mboxMsg {
	seen := make(map[string]bool)
	var msgs []mboxMsg
	seq := uint32(0)
	for _, sub := range []string{"new", "cur"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			key := e.Name()
			if seen[key] {
				continue
			}
			seen[key] = true
			info, err := e.Info()
			if err != nil {
				continue
			}
			seq++
			msgs = append(msgs, mboxMsg{
				uid:   uidFromKey(key),
				seq:   seq,
				path:  filepath.Join(dir, sub, key),
				flags: parseMaildirFlags(key),
				date:  info.ModTime(),
				size:  info.Size(),
			})
		}
	}
	return msgs
}

func forEach(numSet imap.NumSet, userDir string, fn func(m mboxMsg)) {
	msgs := scanDir(userDir)

	if numSet == nil {
		for _, m := range msgs {
			fn(m)
		}
		return
	}

	for _, m := range msgs {
		switch ns := numSet.(type) {
		case imap.SeqSet:
			if ns.Contains(m.seq) {
				fn(m)
			}
		case imap.UIDSet:
			if ns.Contains(imap.UID(m.uid)) {
				fn(m)
			}
		default:
			fn(m)
		}
	}
}

func uidFromKey(key string) uint32 {
	idx := strings.LastIndex(key, ":2,")
	if idx >= 0 {
		key = key[:idx]
	}
	h := uint32(0)
	for _, c := range key {
		h = h*31 + uint32(c)
	}
	return h
}

func uidVal(dir string) uint32 {
	h := uint32(0xdeadbeef)
	for _, c := range dir {
		h = h*31 + uint32(c)
	}
	return h
}

func parseMaildirFlags(key string) []string {
	idx := strings.LastIndex(key, ":2,")
	if idx < 0 {
		return nil
	}
	var f []string
	for _, c := range key[idx+3:] {
		f = append(f, string(c))
	}
	return f
}

func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

func imapFlagsFromMaildir(mf []string) []imap.Flag {
	var f []imap.Flag
	for _, fl := range mf {
		switch fl {
		case "S":
			f = append(f, imap.FlagSeen)
		case "R":
			f = append(f, imap.FlagAnswered)
		case "F":
			f = append(f, imap.FlagFlagged)
		case "T":
			f = append(f, imap.FlagDeleted)
		case "D":
			f = append(f, imap.FlagDraft)
		}
	}
	return f
}

func maildirFlagsFromIMAP(imapFlags []imap.Flag) []string {
	var f []string
	for _, fl := range imapFlags {
		switch fl {
		case imap.FlagSeen:
			f = append(f, "S")
		case imap.FlagAnswered:
			f = append(f, "R")
		case imap.FlagFlagged:
			f = append(f, "F")
		case imap.FlagDeleted:
			f = append(f, "T")
		case imap.FlagDraft:
			f = append(f, "D")
		}
	}
	return f
}

func applyFlags(current []string, store *imap.StoreFlags) []string {
	mdFlags := maildirFlagsFromIMAP(store.Flags)
	switch store.Op {
	case imap.StoreFlagsSet:
		return mdFlags
	case imap.StoreFlagsAdd:
		seen := make(map[string]bool)
		for _, f := range current {
			seen[f] = true
		}
		for _, f := range mdFlags {
			seen[f] = true
		}
		var r []string
		for f := range seen {
			r = append(r, f)
		}
		sort.Strings(r)
		return r
	case imap.StoreFlagsDel:
		remove := make(map[string]bool)
		for _, f := range mdFlags {
			remove[f] = true
		}
		var r []string
		for _, f := range current {
			if !remove[f] {
				r = append(r, f)
			}
		}
		return r
	default:
		return current
	}
}

func updateFlags(path string, flags []string) error {
	base := filepath.Base(path)
	dir := filepath.Dir(path)
	suffix := fmt.Sprintf(":2,%s", strings.Join(flags, ""))
	idx := strings.LastIndex(base, ":2,")
	var newBase string
	if idx >= 0 {
		newBase = base[:idx] + suffix
	} else {
		newBase = base + suffix
	}
	if base == newBase {
		return nil
	}
	return os.Rename(path, filepath.Join(dir, newBase))
}

func copyMsg(srcPath, destDir string) uint32 {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return 0
	}
	del, err := maildir.NewDelivery(destDir)
	if err != nil {
		return 0
	}
	if _, err := io.Copy(del, bytes.NewReader(data)); err != nil {
		del.Abort()
		return 0
	}
	del.Close()
	return uint32(time.Now().UnixNano() % 100000)
}

func extractHeader(data []byte) []byte {
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		return data
	}
	return data[:idx+2]
}

func envelope(data []byte) *imap.Envelope {
	env := &imap.Envelope{Date: time.Now()}
	hdr := extractHeader(data)
	for _, line := range bytes.Split(hdr, []byte("\r\n")) {
		ls := string(line)
		low := strings.ToLower(ls)
		switch {
		case strings.HasPrefix(low, "from:"):
			env.From = parseAddr(ls[5:])
		case strings.HasPrefix(low, "to:"):
			env.To = parseAddr(ls[3:])
		case strings.HasPrefix(low, "subject:"):
			env.Subject = strings.TrimSpace(ls[8:])
		case strings.HasPrefix(low, "message-id:"):
			env.MessageID = strings.Trim(strings.TrimSpace(ls[11:]), "<> ")
		case strings.HasPrefix(low, "date:"):
			if t, err := time.Parse(time.RFC1123Z, strings.TrimSpace(ls[5:])); err == nil {
				env.Date = t
			}
		case strings.HasPrefix(low, "in-reply-to:"):
			if v := strings.Trim(strings.TrimSpace(ls[12:]), "<> "); v != "" {
				env.InReplyTo = []string{v}
			}
		}
	}
	return env
}

func parseAddr(s string) []imap.Address {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var addrs []imap.Address
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if idx := strings.Index(p, "<"); idx >= 0 {
			name := strings.TrimSpace(p[:idx])
			email := strings.TrimRight(p[idx+1:], "> ")
			if at := strings.Index(email, "@"); at > 0 {
				addrs = append(addrs, imap.Address{
					Name:    name,
					Mailbox: email[:at],
					Host:    email[at+1:],
				})
			}
		} else if at := strings.Index(p, "@"); at > 0 {
			addrs = append(addrs, imap.Address{
				Mailbox: p[:at],
				Host:    p[at+1:],
			})
		}
	}
	return addrs
}

func bodyStructure(data []byte) imap.BodyStructure {
	ct := "text/plain"
	for _, line := range bytes.Split(extractHeader(data), []byte("\r\n")) {
		if bytes.HasPrefix(bytes.ToLower(line), []byte("content-type:")) {
			if v := strings.TrimSpace(string(line[13:])); v != "" {
				ct = v
			}
			break
		}
	}
	mimeType, mimeSub := "text", "plain"
	if parts := strings.SplitN(ct, "/", 2); len(parts) == 2 {
		mimeType = strings.TrimSpace(parts[0])
		mimeSub = strings.TrimSpace(parts[1])
	}
	return &imap.BodyStructureSinglePart{
		Type:    mimeType,
		Subtype: mimeSub,
		Size:    uint32(len(data)),
	}
}

var (
	_ imapserver.Session          = (*session)(nil)
	_ imapserver.SessionIMAP4rev2 = (*session)(nil)
)
