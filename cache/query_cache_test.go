package cache

import (
	"net"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
)

func makeCache(maxcount int) *QueryCache {
	return NewQueryCache(maxcount, 0)
}

func Test_Cache(t *testing.T) {
	cache := makeCache(1)
	WallClock = clockwork.NewFakeClock()

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(testDomain), dns.TypeA)

	key := Hash(m.Question[0])

	if err := cache.Set(key, m); err != nil {
		t.Error(err)
	}

	if _, _, err := cache.Get(key, m); err != nil {
		t.Error(err)
	}

	assert.Equal(t, cache.Exists(key), true)

	m2 := new(dns.Msg)
	m2.SetQuestion("test2.com.", dns.TypeA)
	err := cache.Set(Hash(m2.Question[0]), m2)
	assert.Error(t, err)
	assert.Equal(t, err.Error(), "capacity full")

	cache.Remove(key)

	if _, _, err := cache.Get(key, m2); err == nil {
		t.Error("cache entry still existed after remove")
	}

	assert.Equal(t, cache.Exists(key), false)
}

func Test_CacheTTL(t *testing.T) {
	const (
		testDomain = "www.google.com"
	)

	fakeClock := clockwork.NewFakeClock()
	WallClock = fakeClock
	cache := makeCache(0)

	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(testDomain), dns.TypeA)

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(testDomain), dns.TypeA)

	key := Hash(m.Question[0])

	var attl uint32 = 10
	var aaaattl uint32 = 20
	var nsttl uint32 = 10
	nullroute := net.ParseIP("0.0.0.0")
	nullroutev6 := net.ParseIP("0:0:0:0:0:0:0:0")

	a := &dns.A{
		Hdr: dns.RR_Header{
			Name:   testDomain,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    attl,
		},
		A: nullroute}
	m.Answer = append(m.Answer, a)

	aaaa := &dns.AAAA{
		Hdr: dns.RR_Header{
			Name:   testDomain,
			Rrtype: dns.TypeAAAA,
			Class:  dns.ClassINET,
			Ttl:    aaaattl,
		},
		AAAA: nullroutev6}
	m.Answer = append(m.Answer, aaaa)

	ns := &dns.NS{
		Hdr: dns.RR_Header{
			Name:   testDomain,
			Rrtype: dns.TypeNS,
			Class:  dns.ClassINET,
			Ttl:    nsttl,
		},
		Ns: "localhost"}
	m.Ns = append(m.Ns, ns)

	if err := cache.Set(key, m); err != nil {
		t.Error(err)
	}

	msg, _, err := cache.Get(key, req)
	assert.NoError(t, err)

	for _, answer := range msg.Answer {
		switch answer.Header().Rrtype {
		case dns.TypeA:
			assert.Equal(t, attl, answer.Header().Ttl, "TTL should be unchanged")
		case dns.TypeAAAA:
			assert.Equal(t, aaaattl, answer.Header().Ttl, "TTL should be unchanged")
		default:
			t.Error("Unexpected RR type")
		}
	}

	for _, ns := range msg.Ns {
		switch ns.Header().Rrtype {
		case dns.TypeNS:
			assert.Equal(t, nsttl, ns.Header().Ttl, "TTL should be unchanged")
		default:
			t.Error("Unexpected RR type")
		}
	}

	fakeClock.Advance(5 * time.Second)
	msg, _, err = cache.Get(key, req)
	assert.NoError(t, err)

	for _, answer := range msg.Answer {
		switch answer.Header().Rrtype {
		case dns.TypeA:
			assert.Equal(t, attl-5, answer.Header().Ttl, "TTL should be decreased")
		case dns.TypeAAAA:
			assert.Equal(t, aaaattl-5, answer.Header().Ttl, "TTL should be decreased")
		default:
			t.Error("Unexpected RR type")
		}
	}

	for _, ns := range msg.Ns {
		switch ns.Header().Rrtype {
		case dns.TypeNS:
			assert.Equal(t, nsttl-5, ns.Header().Ttl, "TTL should be decreased")
		default:
			t.Error("Unexpected RR type")
		}
	}

	fakeClock.Advance(5 * time.Second)
	msg, _, err = cache.Get(key, req)
	assert.NoError(t, err)

	for _, answer := range msg.Answer {
		switch answer.Header().Rrtype {
		case dns.TypeA:
			assert.Equal(t, uint32(0), answer.Header().Ttl, "TTL should be zero")
		case dns.TypeAAAA:
			assert.Equal(t, aaaattl-10, answer.Header().Ttl, "TTL should be decreased")
		default:
			t.Error("Unexpected RR type")
		}
	}

	for _, ns := range msg.Ns {
		switch ns.Header().Rrtype {
		case dns.TypeNS:
			assert.Equal(t, uint32(0), ns.Header().Ttl, "TTL should be zero")
		default:
			t.Error("Unexpected RR type")
		}
	}

	fakeClock.Advance(1 * time.Second)

	// accessing an expired key will return KeyExpired error
	msg, _, err = cache.Get(key, req)
	if err != nil && err != ErrCacheExpired {
		t.Error(err)
	}
	assert.Equal(t, err.Error(), "cache expired")

	// accessing an expired key will remove it from the cache
	msg, _, err = cache.Get(key, req)
	if err != nil && err != ErrCacheNotFound {
		t.Error("cache entry still existed after expiring - ", err)
	}
	assert.Equal(t, err.Error(), "cache not found")
}

func Test_CacheTTLFrequentPolling(t *testing.T) {
	const (
		testDomain = "www.google.com"
	)

	fakeClock := clockwork.NewFakeClock()
	WallClock = fakeClock
	cache := makeCache(0)

	req := new(dns.Msg)
	req.SetQuestion(dns.Fqdn(testDomain), dns.TypeA)

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(testDomain), dns.TypeA)

	key := Hash(m.Question[0])

	var attl uint32 = 10
	var nsttl uint32 = 5

	nullroute := net.ParseIP("0.0.0.0")
	a := &dns.A{
		Hdr: dns.RR_Header{
			Name:   testDomain,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    attl,
		},
		A: nullroute}
	m.Answer = append(m.Answer, a)

	ns := &dns.NS{
		Hdr: dns.RR_Header{
			Name:   testDomain,
			Rrtype: dns.TypeNS,
			Class:  dns.ClassINET,
			Ttl:    nsttl,
		},
		Ns: "localhost"}
	m.Ns = append(m.Ns, ns)

	if err := cache.Set(key, m); err != nil {
		t.Error(err)
	}

	msg, _, err := cache.Get(key, req)
	assert.NoError(t, err)

	assert.Equal(t, attl, msg.Answer[0].Header().Ttl, "TTL should be unchanged")

	assert.Equal(t, nsttl, msg.Ns[0].Header().Ttl, "TTL should be unchanged")

	//Poll 50 times at 100ms intervals: the TTL should go down by 5s
	for i := 0; i < 50; i++ {
		fakeClock.Advance(100 * time.Millisecond)
		_, _, err := cache.Get(key, req)
		assert.NoError(t, err)
	}

	msg, _, err = cache.Get(key, req)
	assert.NoError(t, err)

	assert.Equal(t, attl-5, msg.Answer[0].Header().Ttl, "TTL should be decreased")

	assert.Equal(t, nsttl-5, msg.Ns[0].Header().Ttl, "TTL should be decreased")

	fakeClock.Advance(1 * time.Second)

	msg, _, err = cache.Get(key, req)
	if err != nil && err != ErrCacheExpired {
		t.Error(err)
	}

	if err := cache.Set(key, m); err != nil {
		t.Error(err)
	}

	fakeClock.Advance(10 * time.Second)

	cache.clear()

	if cache.Length() != 0 {
		t.Error("cache should be clear")
	}
}