package extract

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// errNotClass marks data that is not a Java .class file.
var errNotClass = errors.New("extract: not a Java class file")

// classAPI parses a Java .class file's constant pool and returns a
// human-readable "API surface" for semantic search: the fully-qualified class
// name followed by its distinct UTF-8 symbols (method/field names, type names and
// string literals). It parses only enough of the format (magic, version, constant
// pool, this_class) to do this; method bytecode is ignored. Malformed input
// yields an error rather than a panic.
func classAPI(data []byte) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("extract: class parse panicked: %v", r)
		}
	}()

	r := &reader{b: data}
	if r.u4() != 0xCAFEBABE {
		return "", errNotClass
	}
	r.u2() // minor
	r.u2() // major
	count := int(r.u2())
	if count == 0 {
		return "", errNotClass
	}

	// Constant pool is 1-indexed with count-1 entries; Long/Double take two slots.
	utf8s := make(map[int]string, count)
	classNameIdx := make(map[int]int, count) // Class entry index -> name Utf8 index
	for i := 1; i < count; i++ {
		if r.err != nil {
			return "", errNotClass
		}
		tag := r.u1()
		switch tag {
		case 1: // Utf8
			n := int(r.u2())
			utf8s[i] = r.str(n)
		case 7: // Class -> name_index
			classNameIdx[i] = int(r.u2())
		case 8, 16, 19, 20: // String / MethodType / Module / Package: one u2 index
			r.u2()
		case 3, 4, 9, 10, 11, 12, 17, 18: // Integer/Float/*ref/NameAndType/Dynamic: 4 bytes
			r.u4()
		case 5, 6: // Long / Double: 8 bytes, and occupy two pool slots
			r.u4()
			r.u4()
			i++
		case 15: // MethodHandle: u1 + u2
			r.u1()
			r.u2()
		default:
			return "", errNotClass // unknown tag → not a class we understand
		}
	}
	r.u2() // access_flags
	thisClass := int(r.u2())
	if r.err != nil {
		return "", errNotClass
	}

	var b strings.Builder
	if ni, ok := classNameIdx[thisClass]; ok {
		if name := utf8s[ni]; name != "" {
			b.WriteString(strings.ReplaceAll(name, "/", "."))
			b.WriteByte('\n')
		}
	}
	// Emit the distinct, human-meaningful UTF-8 constants (names + literals),
	// skipping type descriptors like "()V" and "[Ljava/lang/Object;".
	seen := make(map[string]bool)
	for i := 1; i < count; i++ {
		s := utf8s[i]
		if s == "" || seen[s] || !isReadableSymbol(s) {
			continue
		}
		seen[s] = true
		b.WriteString(s)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// isReadableSymbol keeps identifiers, dotted/slashed names and string literals,
// dropping JVM type descriptors and empty/garbage tokens.
func isReadableSymbol(s string) bool {
	if !utf8.ValidString(s) || len(s) < 2 {
		return false
	}
	switch s[0] {
	case '(', '[', ';': // method/type descriptors
		return false
	}
	return true
}

// reader is a minimal big-endian byte cursor that latches an error instead of
// panicking on a short read (callers check r.err).
type reader struct {
	b   []byte
	pos int
	err error
}

func (r *reader) need(n int) bool {
	if r.err != nil || r.pos+n > len(r.b) {
		r.err = errNotClass
		return false
	}
	return true
}

func (r *reader) u1() byte {
	if !r.need(1) {
		return 0
	}
	v := r.b[r.pos]
	r.pos++
	return v
}

func (r *reader) u2() uint16 {
	if !r.need(2) {
		return 0
	}
	v := binary.BigEndian.Uint16(r.b[r.pos:])
	r.pos += 2
	return v
}

func (r *reader) u4() uint32 {
	if !r.need(4) {
		return 0
	}
	v := binary.BigEndian.Uint32(r.b[r.pos:])
	r.pos += 4
	return v
}

func (r *reader) str(n int) string {
	if !r.need(n) {
		return ""
	}
	s := string(r.b[r.pos : r.pos+n])
	r.pos += n
	return s
}
