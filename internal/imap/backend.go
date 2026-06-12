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
	firstUnseen := uint32(0)
	for _, m := range msgs {
		if !hasFlag(m.flags, "S") {
			firstUnseen = m.seq
			break
		}
	}
	return &imap.SelectData{
		UIDNext:           mailboxUIDNext(mboxDir),
		UIDValidity:       uidVal(mboxDir),
		NumMessages:       uint32(len(msgs)),
		FirstUnseenSeqNum: firstUnseen,
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

func (s *session) Subscribe(name string) error   { return nil }
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
		data.UIDNext = mailboxUIDNext(mboxDir)
	}
	if options.UIDValidity {
		data.UIDValidity = uidVal(mboxDir)
	}
	return data, nil
}

func (s *session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	mboxDir := s.mboxPath(mailbox)

	var flags []maildir.Flag
	if options != nil {
		flags = maildirFlagsFromIMAPFlags(options.Flags)
	}

	md := maildir.Dir(mboxDir)
	msg, wc, err := md.Create(flags)
	if err != nil {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Cannot append"}
	}
	if _, err := io.Copy(wc, r); err != nil {
		wc.Close()
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Cannot read message"}
	}
	if err := wc.Close(); err != nil {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "Cannot save message"}
	}

	st := syncUIDs(mboxDir)
	return &imap.AppendData{
		UIDValidity: st.validity,
		UID:         imap.UID(st.keys[msg.Key()]),
	}, nil
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
		if uid := copyMsg(m.path, destDir, m.flags); uid > 0 {
			srcUIDs.AddNum(imap.UID(m.uid))
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
	var srcUIDs, destUIDs imap.UIDSet
	var toExpunge []uint32

	forEach(numSet, s.userDir, func(m mboxMsg) {
		uid := copyMsg(m.path, destDir, m.flags)
		if uid == 0 {
			return
		}
		if err := os.Remove(m.path); err != nil {
			return
		}
		srcUIDs.AddNum(imap.UID(m.uid))
		destUIDs.AddNum(imap.UID(uid))
		toExpunge = append(toExpunge, m.seq)
	})

	if w != nil {
		w.WriteCopyData(&imap.CopyData{
			SourceUIDs:  srcUIDs,
			UIDValidity: uidVal(destDir),
			DestUIDs:    destUIDs,
		})
		// Report expunges high-to-low so sequence numbers stay valid as the
		// client removes them.
		sort.Slice(toExpunge, func(i, j int) bool { return toExpunge[i] > toExpunge[j] })
		for _, seq := range toExpunge {
			w.WriteExpunge(seq)
		}
	}
	return nil
}

func (s *session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	msgs := scanDir(s.userDir)
	// Walk in descending sequence order so each reported expunge does not shift
	// the sequence numbers of messages still to be reported.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if !hasFlag(m.flags, "T") {
			continue
		}
		if uids != nil && !uids.Contains(imap.UID(m.uid)) {
			continue
		}
		if err := os.Remove(m.path); err != nil {
			continue
		}
		if w != nil {
			w.WriteExpunge(m.seq)
		}
	}
	return nil
}

func (s *session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	data := &imap.SearchData{}
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet
	var min, max uint32
	count := uint32(0)

	forEach(nil, s.userDir, func(m mboxMsg) {
		if !matchCriteria(m, criteria) {
			return
		}
		switch kind {
		case imapserver.NumKindSeq:
			seqSet.AddNum(m.seq)
		case imapserver.NumKindUID:
			uidSet.AddNum(imap.UID(m.uid))
		}
		n := m.seq
		if kind == imapserver.NumKindUID {
			n = m.uid
		}
		if min == 0 || n < min {
			min = n
		}
		if n > max {
			max = n
		}
		count++
	})

	switch kind {
	case imapserver.NumKindSeq:
		data.All = seqSet
	case imapserver.NumKindUID:
		data.All = uidSet
	}
	data.Min = min
	data.Max = max
	data.Count = count
	return data, nil
}

// matchCriteria evaluates a (subset of) IMAP SEARCH criteria against a message.
// Supported: ALL (nil), flag/not-flag, UID/seq sets, size (Larger/Smaller),
// internal-date (Since/Before), header/body/text substring, and And/Or/Not
// combinators. Unsupported criteria are treated as matching so SEARCH never
// returns fewer results than it should.
func matchCriteria(m mboxMsg, c *imap.SearchCriteria) bool {
	if c == nil {
		return true
	}

	for _, f := range c.Flag {
		if !hasFlag(m.flags, maildirFlagFromIMAP(f)) {
			return false
		}
	}
	for _, f := range c.NotFlag {
		if hasFlag(m.flags, maildirFlagFromIMAP(f)) {
			return false
		}
	}

	if c.Larger > 0 && m.size <= c.Larger {
		return false
	}
	if c.Smaller > 0 && m.size >= c.Smaller {
		return false
	}

	if !c.Since.IsZero() && m.date.Before(c.Since) {
		return false
	}
	if !c.Before.IsZero() && !m.date.Before(c.Before) {
		return false
	}

	for _, set := range c.SeqNum {
		if !set.Contains(m.seq) {
			return false
		}
	}
	for _, set := range c.UID {
		if !set.Contains(imap.UID(m.uid)) {
			return false
		}
	}

	// Substring matches require reading the message; only do so when asked.
	if len(c.Header) > 0 || len(c.Body) > 0 || len(c.Text) > 0 {
		raw, err := os.ReadFile(m.path)
		if err != nil {
			return false
		}
		lowerAll := strings.ToLower(string(raw))
		header := strings.ToLower(string(extractHeader(raw)))
		for _, h := range c.Header {
			needle := strings.ToLower(h.Key)
			if h.Value != "" {
				needle += ":"
			}
			if !strings.Contains(header, needle) ||
				(h.Value != "" && !strings.Contains(header, strings.ToLower(h.Value))) {
				return false
			}
		}
		for _, b := range c.Body {
			if !strings.Contains(lowerAll, strings.ToLower(b)) {
				return false
			}
		}
		for _, txt := range c.Text {
			if !strings.Contains(lowerAll, strings.ToLower(txt)) {
				return false
			}
		}
	}

	for i := range c.Not {
		if matchCriteria(m, &c.Not[i]) {
			return false
		}
	}
	for _, pair := range c.Or {
		if !matchCriteria(m, &pair[0]) && !matchCriteria(m, &pair[1]) {
			return false
		}
	}

	return true
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
			sectionData := bodySectionData(raw, bs)
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
	clean := filepath.Clean(strings.TrimPrefix(name, "INBOX/"))
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return s.userDir
	}
	return filepath.Join(s.userDir, clean)
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
	st := syncUIDs(dir)

	seen := make(map[string]bool)
	var msgs []mboxMsg
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
			bk := baseKey(key)
			if seen[bk] {
				continue
			}
			uid, ok := st.keys[bk]
			if !ok {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			seen[bk] = true
			msgs = append(msgs, mboxMsg{
				uid:   uid,
				path:  filepath.Join(dir, sub, key),
				flags: parseMaildirFlags(key),
				date:  info.ModTime(),
				size:  info.Size(),
			})
		}
	}

	// IMAP requires sequence numbers to follow ascending UID order.
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].uid < msgs[j].uid })
	for i := range msgs {
		msgs[i].seq = uint32(i + 1)
	}
	return msgs
}

// mailboxUIDNext returns the next UID a new message in this mailbox would get.
func mailboxUIDNext(dir string) imap.UID {
	return imap.UID(syncUIDs(dir).next)
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

// uidVal derives a stable UIDVALIDITY seed from the mailbox path. It is used
// only as the initial value stored in the UID list; thereafter the persisted
// value is authoritative.
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
		if s := maildirFlagFromIMAP(fl); s != "" {
			f = append(f, s)
		}
	}
	return f
}

// maildirFlagFromIMAP maps a single IMAP flag to its maildir letter ("" if the
// flag has no maildir equivalent, e.g. \Recent or custom keywords).
func maildirFlagFromIMAP(fl imap.Flag) string {
	switch fl {
	case imap.FlagSeen:
		return "S"
	case imap.FlagAnswered:
		return "R"
	case imap.FlagFlagged:
		return "F"
	case imap.FlagDeleted:
		return "T"
	case imap.FlagDraft:
		return "D"
	}
	return ""
}

// maildirFlagsFromStrings converts internal maildir flag letters to
// go-maildir's Flag type.
func maildirFlagsFromStrings(flags []string) []maildir.Flag {
	var out []maildir.Flag
	for _, f := range flags {
		if f != "" {
			out = append(out, maildir.Flag(f[0]))
		}
	}
	return out
}

// maildirFlagsFromIMAPFlags converts IMAP flags to go-maildir's Flag type.
func maildirFlagsFromIMAPFlags(imapFlags []imap.Flag) []maildir.Flag {
	return maildirFlagsFromStrings(maildirFlagsFromIMAP(imapFlags))
}

// bodySectionData extracts the requested BODY[...] section from a raw message,
// honouring the HEADER / TEXT specifiers, HeaderFields filters and partial byte
// ranges.
func bodySectionData(raw []byte, bs *imap.FetchItemBodySection) []byte {
	var data []byte
	switch bs.Specifier {
	case imap.PartSpecifierHeader:
		data = extractHeader(raw)
		if len(bs.HeaderFields) > 0 {
			data = filterHeaderFields(data, bs.HeaderFields, false)
		} else if len(bs.HeaderFieldsNot) > 0 {
			data = filterHeaderFields(data, bs.HeaderFieldsNot, true)
		}
	case imap.PartSpecifierText:
		data = extractText(raw)
	default:
		data = raw
	}

	if bs.Partial != nil {
		data = applyPartial(data, bs.Partial.Offset, bs.Partial.Size)
	}
	return data
}

func applyPartial(data []byte, offset, size int64) []byte {
	if offset >= int64(len(data)) {
		return nil
	}
	data = data[offset:]
	if size < int64(len(data)) {
		data = data[:size]
	}
	return data
}

// extractText returns the body of the message (everything after the header/body
// separator).
func extractText(data []byte) []byte {
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		return nil
	}
	return data[idx+4:]
}

// filterHeaderFields keeps (exclude=false) or drops (exclude=true) the named
// header fields from a header block.
func filterHeaderFields(header []byte, fields []string, exclude bool) []byte {
	want := make(map[string]bool, len(fields))
	for _, f := range fields {
		want[strings.ToLower(f)] = true
	}
	var out bytes.Buffer
	for _, line := range bytes.Split(header, []byte("\r\n")) {
		ls := string(line)
		if ls == "" {
			continue
		}
		// Continuation lines start with whitespace; this simple filter keeps a
		// field only by its first line, which is sufficient for the common
		// single-line headers clients request (Subject, From, Date, ...).
		name := ls
		if idx := strings.IndexByte(ls, ':'); idx >= 0 {
			name = ls[:idx]
		}
		_, named := want[strings.ToLower(strings.TrimSpace(name))]
		if named != exclude {
			out.WriteString(ls)
			out.WriteString("\r\n")
		}
	}
	out.WriteString("\r\n")
	return out.Bytes()
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

// copyMsg copies a message file into destDir, preserving its maildir flags, and
// returns the UID assigned to the copy in the destination mailbox (0 on error).
func copyMsg(srcPath, destDir string, flags []string) uint32 {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return 0
	}
	md := maildir.Dir(destDir)
	msg, wc, err := md.Create(maildirFlagsFromStrings(flags))
	if err != nil {
		return 0
	}
	if _, err := io.Copy(wc, bytes.NewReader(data)); err != nil {
		wc.Close()
		return 0
	}
	if err := wc.Close(); err != nil {
		return 0
	}
	return syncUIDs(destDir).keys[msg.Key()]
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
