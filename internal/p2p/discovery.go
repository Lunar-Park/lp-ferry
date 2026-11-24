package p2p

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// Service is the mDNS / Bonjour service type.
const Service = "_ferry._tcp"

// Advertise publishes this node over mDNS on every interface until ctx
// ends. TXT records carry the peer ID and its tiered endpoints, so a
// paired peer can add paths it has not seen before.
func (n *Node) Advertise(ctx context.Context, port int) error {
	txt := []string{"id=" + n.Identity.ID}
	for _, e := range n.AdvertisedEndpoints() {
		txt = append(txt, "ep="+e.String())
	}
	srv, err := zeroconf.Register(n.Identity.ID, Service, "local.", port, txt, nil)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		srv.Shutdown()
	}()
	return nil
}

// Discovered is one peer seen on the local network.
type Discovered struct {
	PeerID    string     `json:"peer_id"`
	Endpoints []Endpoint `json:"endpoints"`
}

// Browse lists Ferry peers advertising on the local network for d.
func Browse(ctx context.Context, d time.Duration) ([]Discovered, error) {
	res, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	bctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	if err := res.Browse(bctx, Service, "local.", entries); err != nil {
		return nil, err
	}
	seen := map[string]*Discovered{}
	var order []string
	for {
		select {
		case <-bctx.Done():
			out := make([]Discovered, 0, len(order))
			for _, id := range order {
				out = append(out, *seen[id])
			}
			return out, nil
		case e, ok := <-entries:
			if !ok {
				entries = nil // closed: wait for the deadline with what we have
				continue
			}
			if e == nil {
				continue
			}
			var id string
			var eps []Endpoint
			for _, t := range e.Text {
				switch {
				case strings.HasPrefix(t, "id="):
					id = t[3:]
				case strings.HasPrefix(t, "ep="):
					if ep, err := ParseEndpoint(t[3:]); err == nil {
						eps = append(eps, ep)
					}
				}
			}
			if id == "" {
				continue
			}
			for _, ip := range e.AddrIPv4 {
				addr := ip.String() + ":" + strconv.Itoa(e.Port)
				t := TierFor(ip, "")
				eps = append(eps, Endpoint{Addr: addr, Tier: t, Badge: badgeFor(t)})
			}
			if seen[id] == nil {
				seen[id] = &Discovered{PeerID: id}
				order = append(order, id)
			}
			seen[id].Endpoints = append(seen[id].Endpoints, eps...)
		}
	}
}
