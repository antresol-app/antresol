package discovery

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
)

// The few pieces of the DNS wire format (RFC 1035) an mDNS responder for
// one service needs: reading the questions of a query, writing PTR, SRV,
// TXT and A records. Names are written uncompressed.

const (
	typeA   = 1
	typePTR = 12
	typeTXT = 16
	typeSRV = 33
	typeANY = 255

	classIN    = 1
	classMask  = 0x7fff // the top bit is mDNS's unicast-response / cache-flush bit
	cacheFlush = 0x8000
	qu         = 0x8000
)

var errMsg = errors.New("discovery: malformed DNS message")

type question struct {
	name  string // lower case, with the trailing dot
	qtype uint16
	qu    bool // the asker wants a unicast answer
}

type query struct {
	id        uint16
	response  bool
	opcode    int
	questions []question
}

// readName reads a (possibly compressed) name at off; it returns the name
// in lower case with a trailing dot and the offset after it.
func readName(msg []byte, off int) (string, int, error) {
	var b strings.Builder
	end := -1
	for jumps := 0; ; {
		if off >= len(msg) {
			return "", 0, errMsg
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			if b.Len() == 0 {
				b.WriteByte('.')
			}
			return strings.ToLower(b.String()), end, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(msg) || jumps > 10 {
				return "", 0, errMsg
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3fff)
			jumps++
		case l&0xc0 != 0:
			return "", 0, errMsg
		default:
			if off+1+l > len(msg) || b.Len()+l > 255 {
				return "", 0, errMsg
			}
			b.Write(msg[off+1 : off+1+l])
			b.WriteByte('.')
			off += 1 + l
		}
	}
}

func parseQuery(msg []byte) (query, error) {
	m, err := parseMessage(msg)
	return m.query, err
}

// rrIn is a resource record read from a message. data is the raw rdata: a
// name inside it may be compressed (only A records are compared as data,
// and they hold none).
type rrIn struct {
	name  string // lower case, with the trailing dot
	rtype uint16
	class uint16 // without the cache-flush bit
	ttl   uint32
	data  []byte
}

// message is a whole mDNS message: the questions and the records of the
// answer, authority (a probe's proposed records) and additional sections.
type message struct {
	query
	answers, authority, extra []rrIn
}

// records are every record of the message, all sections.
func (m message) records() []rrIn {
	return append(append(append([]rrIn(nil), m.answers...), m.authority...), m.extra...)
}

// parseMessage reads a message; at most 32 entries per section are kept.
func parseMessage(msg []byte) (message, error) {
	if len(msg) < 12 {
		return message{}, errMsg
	}
	flags := binary.BigEndian.Uint16(msg[2:])
	var m message
	m.query = query{id: binary.BigEndian.Uint16(msg), response: flags&0x8000 != 0, opcode: int(flags>>11) & 0xf}
	count := func(at int) int { return int(binary.BigEndian.Uint16(msg[at:])) }
	off := 12
	for range min(count(4), 32) {
		name, next, err := readName(msg, off)
		if err != nil || next+4 > len(msg) {
			return message{}, errMsg
		}
		cls := binary.BigEndian.Uint16(msg[next+2:])
		m.questions = append(m.questions, question{name: name, qtype: binary.BigEndian.Uint16(msg[next:]), qu: cls&qu != 0})
		off = next + 4
	}
	if count(4) > 32 { // the rest cannot be found without reading every question
		return m, nil
	}
	read := func(n int) ([]rrIn, error) {
		var out []rrIn
		for range n {
			name, next, err := readName(msg, off)
			if err != nil || next+10 > len(msg) {
				return nil, errMsg
			}
			l := int(binary.BigEndian.Uint16(msg[next+8:]))
			if next+10+l > len(msg) {
				return nil, errMsg
			}
			out = append(out, rrIn{name: name, rtype: binary.BigEndian.Uint16(msg[next:]),
				class: binary.BigEndian.Uint16(msg[next+2:]) & classMask, ttl: binary.BigEndian.Uint32(msg[next+4:]),
				data: msg[next+10 : next+10+l]})
			off = next + 10 + l
		}
		return out, nil
	}
	var err error
	for i, to := range []*[]rrIn{&m.answers, &m.authority, &m.extra} {
		n := count(6 + 2*i)
		if n > 32 {
			break
		}
		if *to, err = read(n); err != nil {
			// a broken record section: the questions still count
			m.answers, m.authority, m.extra = nil, nil, nil
			break
		}
	}
	return m, nil
}

// record is one resource record to write.
type record struct {
	name  string
	rtype uint16
	flush bool // a unique record: the cache-flush bit
	ttl   uint32
	data  []byte
}

func appendName(b []byte, name string) []byte {
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if l == "" {
			continue
		}
		if len(l) > 63 {
			l = l[:63]
		}
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func ptrRecord(name, target string, ttl uint32) record {
	return record{name: name, rtype: typePTR, ttl: ttl, data: appendName(nil, target)}
}

func srvRecord(name, host string, port uint16, ttl uint32) record {
	d := []byte{0, 0, 0, 0, byte(port >> 8), byte(port)} // priority 0, weight 0
	return record{name: name, rtype: typeSRV, flush: true, ttl: ttl, data: appendName(d, host)}
}

func txtRecord(name string, kv []string, ttl uint32) record {
	var d []byte
	for _, s := range kv {
		if len(s) > 255 {
			s = s[:255]
		}
		d = append(d, byte(len(s)))
		d = append(d, s...)
	}
	if len(d) == 0 {
		d = []byte{0}
	}
	return record{name: name, rtype: typeTXT, flush: true, ttl: ttl, data: d}
}

func aRecord(name string, a netip.Addr, ttl uint32) record {
	b := a.As4()
	return record{name: name, rtype: typeA, flush: true, ttl: ttl, data: b[:]}
}

// buildProbe writes a probe (RFC 6762 §8.1): an ANY question for name,
// asking for a unicast answer, with the records we propose in the
// authority section.
func buildProbe(name string, proposed []record) []byte {
	b := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(b[4:], 1)
	binary.BigEndian.PutUint16(b[8:], uint16(len(proposed)))
	b = appendName(b, name)
	b = binary.BigEndian.AppendUint16(b, typeANY)
	b = binary.BigEndian.AppendUint16(b, classIN|qu)
	for _, r := range proposed {
		b = appendRecord(b, r, true)
	}
	return b
}

func appendRecord(b []byte, r record, noFlush bool) []byte {
	b = appendName(b, r.name)
	b = binary.BigEndian.AppendUint16(b, r.rtype)
	cls := uint16(classIN)
	if r.flush && !noFlush {
		cls |= cacheFlush
	}
	b = binary.BigEndian.AppendUint16(b, cls)
	b = binary.BigEndian.AppendUint32(b, r.ttl)
	b = binary.BigEndian.AppendUint16(b, uint16(len(r.data)))
	return append(b, r.data...)
}

// buildResponse writes an authoritative answer: id and questions are
// echoed for a legacy unicast query (RFC 6762 §6.7), else id 0 and none.
func buildResponse(legacy bool, id uint16, qs []question, answers, extra []record) []byte {
	b := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(b, id)
	binary.BigEndian.PutUint16(b[2:], 0x8400) // QR, AA
	binary.BigEndian.PutUint16(b[4:], uint16(len(qs)))
	binary.BigEndian.PutUint16(b[6:], uint16(len(answers)))
	binary.BigEndian.PutUint16(b[10:], uint16(len(extra)))
	for _, q := range qs {
		b = appendName(b, q.name)
		b = binary.BigEndian.AppendUint16(b, q.qtype)
		b = binary.BigEndian.AppendUint16(b, classIN)
	}
	for _, rs := range [][]record{answers, extra} {
		for _, r := range rs {
			b = appendRecord(b, r, legacy) // no cache-flush bit in a legacy unicast answer
		}
	}
	return b
}
