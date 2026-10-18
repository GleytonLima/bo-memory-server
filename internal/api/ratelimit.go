package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// limiter: balde de fichas por chave (IP ou token). Limites folgados — só barram abuso (força bruta
// no login, laço de um cliente com defeito) — e 429 com Retry-After ao estourar.
type limiter struct {
	mu      sync.Mutex
	rate    float64 // fichas por segundo
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute, burst int) *limiter {
	return &limiter{rate: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 10000 { // não cresce sem limite: recomeça (perde só o histórico de quem está ok)
			l.buckets = map[string]*bucket{}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *limiter) wrap(key func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(key(r)) {
			w.Header().Set("Retry-After", strconv.Itoa(int(1/l.rate)+1))
			writeError(w, http.StatusTooManyRequests, "Muitas requisições; tente de novo em instantes.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// byIP: o endereço de quem conecta (atrás de proxy, é o do proxy — o limite fica por instância).
func byIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// byToken: o token (ou o IP, sem token): cada dev tem o próprio balde.
func byToken(r *http.Request) string {
	if t := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); t != "" {
		return "t:" + t
	}
	return "ip:" + byIP(r)
}
