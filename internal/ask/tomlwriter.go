package ask

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/saeta-eth/watermelon/internal/config"
)

// AddDomainToConfig adds a domain to the network.allow list in
// .watermelon.toml. It reports whether the file changed and is a no-op when an
// equivalent host-only rule is already present.
//
// The edit is deliberately surgical: only bytes needed to add the array
// element are inserted, leaving comments and formatting intact. Advisory file
// locking prevents concurrent Watermelon processes from losing each other's
// changes.
func AddDomainToConfig(configPath string, domain string) (bool, error) {
	rule, err := config.ParseNetworkRule(domain)
	if err != nil || rule.Wildcard || rule.Port != 0 {
		if err == nil {
			err = fmt.Errorf("prompted rules must name one host without a wildcard or port")
		}
		return false, fmt.Errorf("invalid prompted network domain %q: %w", domain, err)
	}
	domain = rule.Host

	locked, info, err := lockCurrentFile(configPath)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = syscall.Flock(int(locked.Fd()), syscall.LOCK_UN)
		_ = locked.Close()
	}()

	original, err := io.ReadAll(locked)
	if err != nil {
		return false, fmt.Errorf("reading config: %w", err)
	}
	cfg, err := config.Parse(original)
	if err != nil {
		return false, err
	}
	if err := config.Validate(cfg); err != nil {
		return false, fmt.Errorf("validating config before network.allow edit: %w", err)
	}

	for _, existing := range cfg.Network.Allow {
		existingRule, parseErr := config.ParseNetworkRule(existing)
		if parseErr == nil && !existingRule.Wildcard && existingRule.Port == 0 && existingRule.Host == domain {
			return false, nil
		}
	}

	updated, err := appendNetworkAllow(original, domain)
	if err != nil {
		return false, err
	}

	// Parse the exact candidate before replacing the user's file. Besides
	// catching editor bugs, comparing the decoded list makes source constructs
	// outside the layouts handled below fail closed rather than being rewritten.
	updatedCfg, err := config.Parse(updated)
	if err != nil {
		return false, fmt.Errorf("editing network.allow: %w", err)
	}
	if err := config.Validate(updatedCfg); err != nil {
		return false, fmt.Errorf("validating edited network.allow: %w", err)
	}
	wantAllow := append(slices.Clone(cfg.Network.Allow), domain)
	if !slices.Equal(updatedCfg.Network.Allow, wantAllow) {
		return false, fmt.Errorf("editing network.allow: unsupported TOML layout")
	}

	if err := replaceFileAtomically(configPath, updated, info.Mode()); err != nil {
		return false, err
	}
	return true, nil
}

// lockCurrentFile locks the inode currently named by path. A process may have
// opened the old inode immediately before another process atomically renamed a
// replacement over it. In that case, retry with the new inode rather than
// treating a lock on the stale file as protection for the current config.
func lockCurrentFile(path string) (*os.File, os.FileInfo, error) {
	for {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return nil, nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			_ = f.Close()
			return nil, nil, err
		}

		lockedInfo, err := f.Stat()
		if err != nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			return nil, nil, err
		}
		pathInfo, err := os.Stat(path)
		if err == nil && os.SameFile(lockedInfo, pathInfo) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
				return nil, nil, err
			}
			return f, lockedInfo, nil
		}

		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, err
		}
	}
}

func replaceFileAtomically(path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".watermelon.toml.tmp*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	preservedMode := mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if err = tmp.Chmod(preservedMode); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

type tomlSection struct {
	headerEnd int
	bodyEnd   int
}

type tomlArray struct {
	open  int
	close int
}

func appendNetworkAllow(data []byte, domain string) ([]byte, error) {
	section, allow, err := findNetworkAllow(data)
	if err != nil {
		return nil, err
	}
	if allow != nil {
		return appendStringArray(data, *allow, domain)
	}

	eol := detectEOL(data)
	assignment := []byte(`allow = ["` + domain + `"]` + eol)
	if section != nil {
		prefix := []byte(nil)
		if section.headerEnd > 0 && section.headerEnd == len(data) && !endsInNewline(data[:section.headerEnd]) {
			prefix = []byte(eol)
		}
		return insertBytes(data, section.headerEnd, append(prefix, assignment...)), nil
	}

	var addition strings.Builder
	if len(data) > 0 && !endsInNewline(data) {
		addition.WriteString(eol)
	}
	addition.WriteString("[network]")
	addition.WriteString(eol)
	addition.Write(assignment)
	return append(slices.Clone(data), addition.String()...), nil
}

// findNetworkAllow recognizes Watermelon's normal table/key spelling. More
// exotic TOML remains valid input, but is rejected later by semantic candidate
// verification instead of risking a broad re-encoding.
func findNetworkAllow(data []byte) (*tomlSection, *tomlArray, error) {
	var network *tomlSection
	for start := 0; start < len(data); {
		lineEnd, next := physicalLine(data, start)
		line := data[start:lineEnd]
		if name, ok := simpleTableHeader(line); ok {
			if network != nil && network.bodyEnd == len(data) {
				network.bodyEnd = start
			}
			if name == "network" {
				if network != nil {
					return nil, nil, fmt.Errorf("editing network.allow: duplicate [network] table")
				}
				network = &tomlSection{headerEnd: next, bodyEnd: len(data)}
			}
		}
		start = next
	}

	if network == nil {
		return nil, nil, nil
	}
	for start := network.headerEnd; start < network.bodyEnd; {
		lineEnd, next := physicalLine(data, start)
		valueStart, ok := simpleAllowAssignment(data, start, lineEnd)
		if !ok {
			start = next
			continue
		}
		if valueStart >= network.bodyEnd || data[valueStart] != '[' {
			return nil, nil, fmt.Errorf("editing network.allow: allow must use an array")
		}
		close, err := matchingArrayClose(data, valueStart, network.bodyEnd)
		if err != nil {
			return nil, nil, fmt.Errorf("editing network.allow: %w", err)
		}
		return network, &tomlArray{open: valueStart, close: close}, nil
	}
	return network, nil, nil
}

func physicalLine(data []byte, start int) (lineEnd, next int) {
	if nl := bytes.IndexByte(data[start:], '\n'); nl >= 0 {
		lineEnd = start + nl
		if lineEnd > start && data[lineEnd-1] == '\r' {
			lineEnd--
		}
		return lineEnd, start + nl + 1
	}
	return len(data), len(data)
}

func simpleTableHeader(line []byte) (string, bool) {
	line = bytes.TrimSpace(line)
	if len(line) < 3 || line[0] != '[' || line[1] == '[' {
		return "", false
	}
	close := bytes.IndexByte(line, ']')
	if close < 0 {
		return "", false
	}
	tail := bytes.TrimSpace(line[close+1:])
	if len(tail) > 0 && tail[0] != '#' {
		return "", false
	}
	name := strings.TrimSpace(string(line[1:close]))
	if name == "" || strings.ContainsAny(name, ".\"'") {
		return "", false
	}
	return name, true
}

func simpleAllowAssignment(data []byte, start, end int) (int, bool) {
	i := start
	for i < end && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	const key = "allow"
	if i+len(key) > end || string(data[i:i+len(key)]) != key {
		return 0, false
	}
	i += len(key)
	if i < end && data[i] != ' ' && data[i] != '\t' && data[i] != '=' {
		return 0, false
	}
	for i < end && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	if i >= end || data[i] != '=' {
		return 0, false
	}
	i++
	for i < end && (data[i] == ' ' || data[i] == '\t') {
		i++
	}
	return i, true
}

func matchingArrayClose(data []byte, open, limit int) (int, error) {
	depth := 0
	for i := open; i < limit; {
		switch data[i] {
		case '"', '\'':
			next, err := skipTOMLString(data, i, limit)
			if err != nil {
				return 0, err
			}
			i = next
		case '#':
			if nl := bytes.IndexByte(data[i:limit], '\n'); nl >= 0 {
				i += nl + 1
			} else {
				return 0, fmt.Errorf("unterminated array")
			}
		case '[':
			depth++
			i++
		case ']':
			depth--
			if depth == 0 {
				return i, nil
			}
			if depth < 0 {
				return 0, fmt.Errorf("unexpected array close")
			}
			i++
		default:
			i++
		}
	}
	return 0, fmt.Errorf("unterminated array")
}

func skipTOMLString(data []byte, start, limit int) (int, error) {
	quote := data[start]
	multiline := start+2 < limit && data[start+1] == quote && data[start+2] == quote
	if multiline {
		for i := start + 3; i < limit; {
			if quote == '"' && data[i] == '\\' {
				i += 2
				continue
			}
			if data[i] == quote {
				run := 1
				for i+run < limit && data[i+run] == quote {
					run++
				}
				if run >= 3 {
					return i + run, nil
				}
				i += run
				continue
			}
			i++
		}
		return 0, fmt.Errorf("unterminated multiline string")
	}

	for i := start + 1; i < limit; i++ {
		if quote == '"' && data[i] == '\\' {
			i++
			continue
		}
		if data[i] == quote {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string")
}

type arrayLayout struct {
	count         int
	lastStart     int
	lastEnd       int
	lastCommaEnd  int
	trailingComma bool
}

func appendStringArray(data []byte, array tomlArray, domain string) ([]byte, error) {
	layout, err := inspectStringArray(data, array)
	if err != nil {
		return nil, fmt.Errorf("editing network.allow: %w", err)
	}
	host := `"` + domain + `"`
	multiline := bytes.ContainsAny(data[array.open+1:array.close], "\r\n")

	var inserts []byteInsertion
	if layout.count > 0 && !layout.trailingComma {
		inserts = append(inserts, byteInsertion{at: layout.lastEnd, text: ","})
	}
	if multiline {
		lineStart := bytes.LastIndexByte(data[:array.close], '\n') + 1
		if onlyHorizontalSpace(data[lineStart:array.close]) {
			indent := string(data[lineStart:array.close]) + "    "
			if layout.count > 0 {
				valueLine := bytes.LastIndexByte(data[:layout.lastStart], '\n') + 1
				if onlyHorizontalSpace(data[valueLine:layout.lastStart]) {
					indent = string(data[valueLine:layout.lastStart])
				}
			}
			eol := "\n"
			if lineStart >= 2 && data[lineStart-2] == '\r' {
				eol = "\r\n"
			}
			inserts = append(inserts, byteInsertion{at: lineStart, text: indent + host + "," + eol})
			return applyInsertions(data, inserts), nil
		}
	}

	if layout.count == 0 {
		inserts = append(inserts, byteInsertion{at: array.close, text: host})
	} else {
		gapStart := layout.lastEnd
		if layout.trailingComma {
			gapStart = layout.lastCommaEnd
		}
		prefix := ""
		if gapStart == array.close {
			prefix = " "
		}
		inserts = append(inserts, byteInsertion{at: array.close, text: prefix + host})
	}
	return applyInsertions(data, inserts), nil
}

func inspectStringArray(data []byte, array tomlArray) (arrayLayout, error) {
	layout := arrayLayout{lastStart: -1, lastEnd: -1, lastCommaEnd: -1}
	i := array.open + 1
	for {
		i = skipArrayTrivia(data, i, array.close)
		if i == array.close {
			return layout, nil
		}
		if i > array.close || (data[i] != '"' && data[i] != '\'') {
			return arrayLayout{}, fmt.Errorf("allow array contains a non-string value")
		}
		start := i
		end, err := skipTOMLString(data, i, array.close)
		if err != nil {
			return arrayLayout{}, err
		}
		layout.count++
		layout.lastStart = start
		layout.lastEnd = end
		layout.lastCommaEnd = -1
		layout.trailingComma = false
		i = skipArrayTrivia(data, end, array.close)
		if i == array.close {
			return layout, nil
		}
		if data[i] != ',' {
			return arrayLayout{}, fmt.Errorf("allow array elements are not comma-separated")
		}
		layout.lastCommaEnd = i + 1
		layout.trailingComma = true
		i++
		next := skipArrayTrivia(data, i, array.close)
		if next == array.close {
			return layout, nil
		}
		i = next
	}
}

func skipArrayTrivia(data []byte, start, limit int) int {
	for start < limit {
		switch data[start] {
		case ' ', '\t', '\r', '\n':
			start++
		case '#':
			if nl := bytes.IndexByte(data[start:limit], '\n'); nl >= 0 {
				start += nl + 1
			} else {
				return limit
			}
		default:
			return start
		}
	}
	return start
}

type byteInsertion struct {
	at   int
	text string
}

func applyInsertions(data []byte, inserts []byteInsertion) []byte {
	// A compact non-empty array adds both the separating comma and the new
	// value at the old closing-bracket offset. Keep their declaration order so
	// the comma is emitted first.
	slices.SortStableFunc(inserts, func(a, b byteInsertion) int { return a.at - b.at })
	capacity := len(data)
	for _, insertion := range inserts {
		capacity += len(insertion.text)
	}
	out := make([]byte, 0, capacity)
	last := 0
	for _, insertion := range inserts {
		out = append(out, data[last:insertion.at]...)
		out = append(out, insertion.text...)
		last = insertion.at
	}
	return append(out, data[last:]...)
}

func insertBytes(data []byte, at int, insertion []byte) []byte {
	out := make([]byte, 0, len(data)+len(insertion))
	out = append(out, data[:at]...)
	out = append(out, insertion...)
	return append(out, data[at:]...)
}

func onlyHorizontalSpace(data []byte) bool {
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\r' {
			return false
		}
	}
	return true
}

func endsInNewline(data []byte) bool {
	return len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r')
}

func detectEOL(data []byte) string {
	if bytes.Contains(data, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}
