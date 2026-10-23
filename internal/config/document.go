package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// UpdateDocument changes only settings whose decoded values have changed.
// Unchanged settings retain their exact spelling, whitespace and comments.
// Comments inside a replaced value are retained beside the replacement.
// The exact result is decoded and checked before it can be written to disk.
func UpdateDocument(original []byte, cfg *Config) ([]byte, error) {
	before, err := Parse(original)
	if err != nil {
		return nil, err
	}
	oldData, err := encodeDocument(before)
	if err != nil {
		return nil, err
	}
	newData, err := encodeDocument(cfg)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(oldData, newData) {
		return bytes.Clone(original), nil
	}
	var oldValues, newValues map[string]any
	if _, err := toml.Decode(string(oldData), &oldValues); err != nil {
		return nil, err
	}
	if _, err := toml.Decode(string(newData), &newValues); err != nil {
		return nil, err
	}
	statements, err := documentStatements(original)
	if err != nil {
		return nil, err
	}
	desired, err := documentStatements(newData)
	if err != nil {
		return nil, err
	}
	eol := "\n"
	if bytes.Contains(original, []byte("\r\n")) {
		eol = "\r\n"
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	var covered [][]string
	tables := []documentStatement{{end: 0}}
	for _, s := range statements {
		old, _ := documentValue(oldValues, s.path)
		value, exists := documentValue(newValues, s.path)
		if s.table {
			if !exists {
				edits = append(edits, edit{s.start, s.end, ""})
			} else {
				tables = append(tables, s)
			}
			continue
		}
		covered = append(covered, s.path)
		if reflect.DeepEqual(old, value) && exists {
			continue
		}
		if !exists {
			edits = append(edits, edit{s.start, s.end, documentComments(original[s.value:s.end], eol)})
			continue
		}
		replacement, err := documentLiteral(value)
		if err != nil {
			return nil, err
		}
		comments := documentComments(original[s.value:s.end], eol)
		if comments != "" {
			replacement += eol + comments
		}
		edits = append(edits, edit{s.value, s.end, replacement})
	}
	// Insert new keys into their nearest existing table. Dotted keys let us
	// preserve files that use implicit tables rather than normalizing them.
	insertions := map[int]string{}
	for _, s := range desired {
		if s.table {
			continue
		}
		value, _ := documentValue(newValues, s.path)
		old, exists := documentValue(oldValues, s.path)
		if exists && reflect.DeepEqual(old, value) || documentCovered(covered, s.path) {
			continue
		}
		parent := s.path[:len(s.path)-1]
		// An array-of-tables entry must have its own header, including when
		// that entry did not exist in the old document.
		for i, part := range parent {
			if !strings.HasPrefix(part, "\x00") {
				continue
			}
			entry := parent[:i+1]
			found := false
			for _, table := range tables {
				found = found || reflect.DeepEqual(table.path, entry)
			}
			if !found {
				insertions[len(original)] += eol + "[[" + documentKey(parent[:i]) + "]]" + eol
				tables = append(tables, documentStatement{path: append([]string(nil), entry...), end: len(original), virtual: true})
			}
		}
		best := tables[0]
		for _, table := range tables[1:] {
			if len(table.path) > len(best.path) && documentPrefix(table.path, parent) {
				best = table
			}
		}
		at := best.end
		if !best.virtual {
			at = len(original)
			for _, statement := range statements {
				if statement.table && statement.start >= best.end {
					at = bytes.LastIndexByte(original[:statement.start], '\n') + 1
					break
				}
			}
		}
		literal, err := documentLiteral(value)
		if err != nil {
			return nil, err
		}
		insertions[at] += documentKey(s.path[len(best.path):]) + " = " + literal + eol
	}
	for at, addition := range insertions {
		if at > 0 && original[at-1] != '\n' {
			addition = eol + addition
		}
		edits = append(edits, edit{at, at, addition})
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end < edits[j].end
		}
		return edits[i].start < edits[j].start
	})
	var out bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.start < last {
			return nil, fmt.Errorf("cannot preserve overlapping TOML settings")
		}
		out.Write(original[last:e.start])
		out.WriteString(e.text)
		last = e.end
	}
	out.Write(original[last:])
	parsed, err := Parse(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("cannot preserve this TOML layout: %w", err)
	}
	actual, err := encodeDocument(parsed)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(actual, newData) {
		return nil, fmt.Errorf("cannot preserve this TOML layout without changing other settings")
	}
	return out.Bytes(), nil
}

func encodeDocument(cfg *Config) ([]byte, error) {
	var out bytes.Buffer
	err := toml.NewEncoder(&out).Encode(cfg)
	return out.Bytes(), err
}

type documentStatement struct {
	path              []string
	start, value, end int
	table, virtual    bool
}

// documentStatements only locates syntactic boundaries. BurntSushi/toml is
// always the authority for syntax and values; this scanner never accepts an
// edited document on its own.
func documentStatements(data []byte) ([]documentStatement, error) {
	var result []documentStatement
	var table []string
	arrays := map[string]int{}
	for i := 0; i < len(data); {
		if strings.ContainsRune(" \t\r\n", rune(data[i])) {
			i++
			continue
		}
		if data[i] == '#' {
			i = documentLineEnd(data, i)
			continue
		}
		start := i
		if data[i] == '[' {
			array := i+1 < len(data) && data[i+1] == '['
			i++
			if array {
				i++
			}
			keyStart := i
			for i < len(data) && data[i] != ']' {
				if data[i] == '"' || data[i] == '\'' {
					i = documentStringEnd(data, i)
				} else {
					i++
				}
			}
			key, err := documentParseKey(string(data[keyStart:i]))
			if err != nil || i >= len(data) {
				return nil, fmt.Errorf("cannot locate TOML table: %v", err)
			}
			i++
			if array {
				i++
			}
			table = key
			if array {
				id := strings.Join(key, "\x00")
				index := arrays[id]
				arrays[id]++
				table = append(table, "\x00"+strconv.Itoa(index))
			}
			result = append(result, documentStatement{path: append([]string(nil), table...), start: start, end: i, table: true})
			continue
		}
		for i < len(data) && data[i] != '=' {
			if data[i] == '"' || data[i] == '\'' {
				i = documentStringEnd(data, i)
			} else {
				i++
			}
		}
		key, err := documentParseKey(string(data[start:i]))
		if err != nil || i >= len(data) {
			return nil, fmt.Errorf("cannot locate TOML setting: %v", err)
		}
		i++
		for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
			i++
		}
		value := i
		depth := 0
		for i < len(data) {
			c := data[i]
			if c == '"' || c == '\'' {
				i = documentStringEnd(data, i)
				continue
			}
			if c == '#' {
				if depth == 0 {
					break
				}
				i = documentLineEnd(data, i)
				continue
			}
			if depth == 0 && (c == '\n' || c == '\r') {
				break
			}
			if c == '[' || c == '{' {
				depth++
			}
			if c == ']' || c == '}' {
				depth--
			}
			i++
		}
		end := i
		for end > value && (data[end-1] == ' ' || data[end-1] == '\t') {
			end--
		}
		result = append(result, documentStatement{path: append(append([]string(nil), table...), key...), start: start, value: value, end: end})
	}
	return result, nil
}

func documentParseKey(key string) ([]string, error) {
	var parts []string
	data := []byte(strings.TrimSpace(key))
	for i := 0; i < len(data); {
		start := i
		if data[i] == '\'' || data[i] == '"' {
			i = documentStringEnd(data, i)
			var value struct{ Key string }
			if _, err := toml.Decode("key = "+string(data[start:i]), &value); err != nil {
				return nil, err
			}
			parts = append(parts, value.Key)
		} else {
			for i < len(data) && data[i] != '.' {
				i++
			}
			parts = append(parts, strings.TrimSpace(string(data[start:i])))
		}
		for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
			i++
		}
		if i < len(data) {
			if data[i] != '.' {
				return nil, fmt.Errorf("invalid dotted key")
			}
			i++
			for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
				i++
			}
		}
	}
	return parts, nil
}

func documentStringEnd(data []byte, start int) int {
	quote := data[start]
	multi := start+2 < len(data) && data[start+1] == quote && data[start+2] == quote
	i := start + 1
	if multi {
		i = start + 3
	}
	for i < len(data) {
		if quote == '"' && data[i] == '\\' {
			i += 2
			continue
		}
		if data[i] == quote {
			if !multi {
				return i + 1
			}
			end := i
			for end < len(data) && data[end] == quote {
				end++
			}
			if end-i >= 3 {
				return end
			}
			i = end
			continue
		}
		i++
	}
	return len(data)
}

func documentLineEnd(data []byte, start int) int {
	if n := bytes.IndexByte(data[start:], '\n'); n >= 0 {
		return start + n
	}
	return len(data)
}

func documentValue(root any, path []string) (any, bool) {
	for _, part := range path {
		if strings.HasPrefix(part, "\x00") {
			index, _ := strconv.Atoi(part[1:])
			items, ok := root.([]map[string]any)
			if !ok || index >= len(items) {
				return nil, false
			}
			root = items[index]
		} else {
			values, ok := root.(map[string]any)
			if !ok {
				return nil, false
			}
			var exists bool
			root, exists = values[part]
			if !exists {
				return nil, false
			}
		}
	}
	return root, true
}

func documentPrefix(prefix, path []string) bool {
	return len(prefix) <= len(path) && reflect.DeepEqual(prefix, path[:len(prefix)])
}

func documentCovered(paths [][]string, key []string) bool {
	for _, path := range paths {
		if documentPrefix(path, key) {
			return true
		}
	}
	return false
}

func documentKey(parts []string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = documentQuote(part)
	}
	return strings.Join(quoted, ".")
}

func documentLiteral(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return documentQuote(v), nil
	case bool, int64, float64:
		return fmt.Sprint(v), nil
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			literal, err := documentLiteral(item)
			if err != nil {
				return "", err
			}
			parts[i] = literal
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	case []map[string]any:
		items := make([]any, len(v))
		for i := range v {
			items[i] = v[i]
		}
		return documentLiteral(items)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			literal, err := documentLiteral(v[key])
			if err != nil {
				return "", err
			}
			parts = append(parts, documentQuote(key)+" = "+literal)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}
	return "", fmt.Errorf("unsupported TOML value %T", value)
}

// JSON string escapes are also TOML basic-string escapes. strconv.Quote can
// emit Go-only escapes such as \x1b, which TOML rejects.
func documentQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func documentComments(data []byte, eol string) string {
	var out strings.Builder
	for i := 0; i < len(data); {
		switch data[i] {
		case '\'', '"':
			i = documentStringEnd(data, i)
		case '#':
			end := documentLineEnd(data, i)
			out.Write(bytes.TrimSuffix(data[i:end], []byte{'\r'}))
			out.WriteString(eol)
			i = end
		default:
			i++
		}
	}
	return out.String()
}
