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
	if len(msg) < 12 {
		return query{}, errMsg
	}
	flags := binary.BigEndian.Uint16(msg[2:])
	q := query{id: binary.BigEndian.Uint16(msg), response: flags&0x8000 != 0, opcode: int(flags>>11) & 0xf}
	n := int(binary.BigEndian.Uint16(msg[4:]))
	off := 12
	for range min(n, 32) {
		name, next, err := readName(msg, off)
		if err != nil || next+4 > len(msg) {
			return query{}, errMsg
		}
		cls := binary.BigEndian.Uint16(msg[next+2:])
		q.questions = append(q.questions, question{name: name, qtype: binary.BigEndian.Uint16(msg[next:]), qu: cls&qu != 0})
		off = next + 4
	}
	return q, nil
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
			b = appendName(b, r.name)
			b = binary.BigEndian.AppendUint16(b, r.rtype)
			cls := uint16(classIN)
			if r.flush && !legacy { // never in a legacy unicast answer
				cls |= cacheFlush
			}
			b = binary.BigEndian.AppendUint16(b, cls)
			b = binary.BigEndian.AppendUint32(b, r.ttl)
			b = binary.BigEndian.AppendUint16(b, uint16(len(r.data)))
			b = append(b, r.data...)
		}
	}
	return b
}
