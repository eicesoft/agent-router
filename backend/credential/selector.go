package credential

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Fingerprint derives a stable session key from content that does not change
// across a conversation's turns: the first system message and the first user
// message. Clients resend the whole history every turn, so any later message
// changes every turn and would break stickiness; the head of the conversation
// is the part that stays identical.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// pickLocked chooses one credential following mode. Callers hold p.mu.
//
// Every strategy first narrows to the usable set (enabled, not permanently
// invalid, not cooling down); the strategies differ only in how they order what
// remains. When the usable set is empty the pool degrades once — ignoring
// cool-downs but still excluding disabled and invalid keys — rather than
// failing, because an all-rate-limited pool should still attempt the key with
// the most remaining headroom.
func (p *Pool) pickLocked(items []Credential, mode Mode, session string, now time.Time) (Credential, bool) {
	usable := make([]Credential, 0, len(items))
	for _, item := range items {
		if p.usableLocked(item, now) {
			usable = append(usable, item)
		}
	}
	if len(usable) == 0 {
		usable = p.degradedLocked(items)
		if len(usable) == 0 {
			return Credential{}, false
		}
	}

	switch mode {
	case ModeSession:
		if session != "" {
			if binding, ok := p.sessions[session]; ok && binding.expiresAt.After(now) {
				for _, item := range usable {
					if item.ID == binding.credentialID {
						p.sessions[session] = sessionBinding{credentialID: item.ID, expiresAt: now.Add(sessionTTL)}
						return item, true
					}
				}
				// The pinned key became unusable: re-pick and rebind below.
			}
		}
		chosen := p.leastUsedLocked(usable)
		if session != "" {
			p.sessions[session] = sessionBinding{credentialID: chosen.ID, expiresAt: now.Add(sessionTTL)}
		}
		return chosen, true
	case ModeLeastUsed:
		return p.leastUsedLocked(usable), true
	case ModeRandom:
		return usable[randomIndex(len(usable))], true
	default: // ModeRoundRobin
		providerID := items[0].ProviderID
		cursor := p.cursors[providerID] % len(usable)
		p.cursors[providerID] = cursor + 1
		return usable[cursor], true
	}
}

// usableLocked reports whether a credential may serve a request right now.
func (p *Pool) usableLocked(item Credential, now time.Time) bool {
	if !item.Enabled || item.Status != StatusActive {
		return false
	}
	if state, ok := p.state[item.ID]; ok && state.cooldownUntil.After(now) {
		return false
	}
	return true
}

// degradedLocked is the single fallback: every key that is not disabled or
// permanently invalid, regardless of cool-down. Ordered least-used first so the
// least degraded key is attempted.
func (p *Pool) degradedLocked(items []Credential) []Credential {
	out := make([]Credential, 0, len(items))
	for _, item := range items {
		if item.Enabled && item.Status == StatusActive {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return []Credential{p.leastUsedLocked(out)}
}

// leastUsedLocked orders by requests per unit weight, breaking ties by position
// in the slice, which is creation order. Request count — not token count — is
// the ranking key because quota windows (RPM) are counted in requests; the
// weight lets an operator express that one key has more headroom than another.
//
// The comparison is strictly less-than so the earliest credential wins a tie:
// created_at only has second resolution, so ordering by it would be arbitrary
// for keys added within the same second.
func (p *Pool) leastUsedLocked(items []Credential) Credential {
	chosen := items[0]
	best := p.loadLocked(chosen)
	for _, item := range items[1:] {
		if load := p.loadLocked(item); load < best {
			chosen, best = item, load
		}
	}
	return chosen
}

// loadLocked is requests divided by weight; the weight is floored at 1 so a
// zero-cost key can never monopolise the pool.
func (p *Pool) loadLocked(item Credential) float64 {
	requests := 0
	if state, ok := p.state[item.ID]; ok {
		requests = state.requests
	}
	weight := item.Weight
	if weight < 1 {
		weight = 1
	}
	return float64(requests) / float64(weight)
}

// SessionHint extracts the caller's session identity from the transport-level
// candidates, most specific first. Returning "" means the caller falls back to
// a content fingerprint.
func SessionHint(headers map[string]string) string {
	for _, key := range []string{"X-Session-Id", "X-Conversation-Id", "X-Client-Request-Id"} {
		if value := strings.TrimSpace(headers[key]); value != "" {
			return value
		}
	}
	return ""
}
