package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Path tiers, fastest first.
const (
	TierDirect    = 1 // direct link / Thunderbolt bridge / 10GbE+
	TierLAN       = 2 // wired 1–2.5GbE
	TierWiFi      = 3
	TierTailscale = 4 // Tailscale / WireGuard direct
	TierWAN       = 5 // configured WAN address
)

// Endpoint is one way to reach a peer.
type Endpoint struct {
	Addr  string `json:"addr"`
	Tier  int    `json:"tier"`
	Badge string `json:"badge"`
}

// String encodes an endpoint for pairing codes and the peers table.
func (e Endpoint) String() string {
	q := url.Values{}
	q.Set("tier", strconv.Itoa(e.Tier))
	if e.Badge != "" {
		q.Set("badge", e.Badge)
	}
	return "tcp://" + e.Addr + "?" + q.Encode()
}

// ParseEndpoint decodes Endpoint.String (a bare host:port is tier LAN).
func ParseEndpoint(s string) (Endpoint, error) {
	if !strings.Contains(s, "://") {
		if _, _, err := net.SplitHostPort(s); err != nil {
			return Endpoint{}, err
		}
		return Endpoint{Addr: s, Tier: TierLAN, Badge: badgeFor(TierLAN)}, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "tcp" || u.Host == "" {
		return Endpoint{}, fmt.Errorf("bad endpoint %q", s)
	}
	tier, _ := strconv.Atoi(u.Query().Get("tier"))
	if tier < TierDirect || tier > TierWAN {
		tier = TierLAN
	}
	badge := u.Query().Get("badge")
	if badge == "" {
		badge = badgeFor(tier)
	}
	return Endpoint{Addr: u.Host, Tier: tier, Badge: badge}, nil
}

func badgeFor(tier int) string {
	switch tier {
	case TierDirect:
		return "10GbE Direct"
	case TierLAN:
		return "LAN"
	case TierWiFi:
		return "Wi-Fi"
	case TierTailscale:
		return "Tailscale Direct"
	default:
		return "WAN"
	}
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// TierFor classifies an address on an interface.
func TierFor(ip net.IP, iface string) int {
	switch {
	case ip == nil:
		return TierWAN
	case cgnat.Contains(ip) || strings.HasPrefix(iface, "tailscale") || (strings.HasPrefix(iface, "utun") && cgnat.Contains(ip)):
		return TierTailscale
	case strings.HasPrefix(iface, "bridge") || strings.HasPrefix(iface, "thunderbolt"):
		return TierDirect
	case ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast():
		if s := linkSpeedMbps(iface); s >= 10000 {
			return TierDirect
		}
		if wireless(iface) {
			return TierWiFi
		}
		return TierLAN
	}
	return TierWAN
}

func linkSpeedMbps(iface string) int {
	if iface == "" {
		return 0
	}
	b, err := os.ReadFile("/sys/class/net/" + iface + "/speed")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}

func wireless(iface string) bool {
	if iface == "" {
		return false
	}
	_, err := os.Stat("/sys/class/net/" + iface + "/wireless")
	return err == nil
}

// LocalEndpoints enumerates this machine's non-loopback addresses with the
// given port, classified by tier.
func LocalEndpoints(port string) []Endpoint {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Endpoint
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			t := TierFor(ipn.IP, ifc.Name)
			out = append(out, Endpoint{Addr: net.JoinHostPort(ipn.IP.String(), port), Tier: t, Badge: badgeFor(t)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Tier < out[j].Tier || (out[i].Tier == out[j].Tier && out[i].Addr < out[j].Addr)
	})
	return out
}

// Measurement is one Path Radar probe.
type Measurement struct {
	Endpoint
	RTT time.Duration `json:"rtt"`
	BPS float64       `json:"bps"`
	Err string        `json:"error,omitempty"`
}

// Radar picks the fastest reachable path to one peer and fails over.
type Radar struct {
	Probe   func(ctx context.Context, ep Endpoint) Measurement
	Timeout time.Duration

	mu     sync.Mutex
	cands  []Endpoint
	active *Endpoint
	down   map[string]time.Time
	last   []Measurement
}

// ErrNoPath means no candidate endpoint answered.
var ErrNoPath = errors.New("no reachable path to peer")

// SetCandidates replaces the endpoint list (deduplicated by address).
func (r *Radar) SetCandidates(eps []Endpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	r.cands = r.cands[:0]
	for _, e := range eps {
		if !seen[e.Addr] {
			seen[e.Addr] = true
			r.cands = append(r.cands, e)
		}
	}
}

// Active returns the selected endpoint, if any.
func (r *Radar) Active() (Endpoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return Endpoint{}, false
	}
	return *r.active, true
}

// Last returns the most recent probe results.
func (r *Radar) Last() []Measurement {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Measurement(nil), r.last...)
}

// Select probes every live candidate in parallel and keeps the best:
// lowest tier that answered, then highest measured throughput, then RTT.
// Paths marked down are skipped unless nothing else answers — a brief drop
// on the only path is retried rather than declared fatal.
func (r *Radar) Select(ctx context.Context) (Endpoint, error) {
	r.mu.Lock()
	var live, down []Endpoint
	for _, c := range r.cands {
		if t, ok := r.down[c.Addr]; ok && time.Since(t) < 30*time.Second {
			down = append(down, c)
		} else {
			live = append(live, c)
		}
	}
	r.mu.Unlock()
	best, results, err := r.race(ctx, live)
	if err != nil && len(down) > 0 {
		var more []Measurement
		best, more, err = r.race(ctx, down)
		results = append(results, more...)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = results
	if err != nil {
		r.active = nil
		return Endpoint{}, err
	}
	delete(r.down, best.Addr)
	r.active = &best
	return best, nil
}

func (r *Radar) race(ctx context.Context, cands []Endpoint) (Endpoint, []Measurement, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	results := make([]Measurement, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func(i int, c Endpoint) {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results[i] = r.Probe(pctx, c)
			results[i].Endpoint = c
		}(i, c)
	}
	wg.Wait()
	var ok []Measurement
	for _, m := range results {
		if m.Err == "" {
			ok = append(ok, m)
		}
	}
	if len(ok) == 0 {
		return Endpoint{}, results, fmt.Errorf("%w (%d candidates)", ErrNoPath, len(cands))
	}
	sort.SliceStable(ok, func(i, j int) bool {
		a, b := ok[i], ok[j]
		if a.Tier != b.Tier {
			return a.Tier < b.Tier
		}
		if a.BPS != b.BPS {
			return a.BPS > b.BPS
		}
		return a.RTT < b.RTT
	})
	return ok[0].Endpoint, results, nil
}

// MarkDown takes an endpoint out of rotation (for 30s) after it failed
// mid-transfer, and selects the next best path.
func (r *Radar) MarkDown(ctx context.Context, ep Endpoint) (Endpoint, error) {
	r.mu.Lock()
	if r.down == nil {
		r.down = map[string]time.Time{}
	}
	r.down[ep.Addr] = time.Now()
	if r.active != nil && r.active.Addr == ep.Addr {
		r.active = nil
	}
	r.mu.Unlock()
	return r.Select(ctx)
}
