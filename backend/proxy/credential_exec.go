package proxy

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent-router/backend/credential"
	"agent-router/backend/provider"
)

// maxCredentialAttempts bounds failover: one retry on a different key. A deeper
// chain would multiply a slow upstream's latency and risk duplicate billing for
// a request the upstream may already have accepted.
const maxCredentialAttempts = 2

// requestCredential records which pooled key served a request so the usage log
// can attribute it. logEvent is defined before the key is chosen and also runs
// on paths that never reach the pool (no key configured), so the zero value
// legitimately means "no credential served this".
type requestCredential struct {
	id   string
	name string
	// mask is the display form of the key (ss****sfg), shown in the request log
	// in place of the credential id. It is safe to persist.
	mask string
}

func (c *requestCredential) set(lease credential.Lease) {
	if c == nil {
		return
	}
	c.id, c.name, c.mask = lease.CredentialID, lease.CredentialName, lease.Mask
}

// sessionIdentity derives the session a request belongs to. An explicit client
// header wins; otherwise the conversation head is fingerprinted. The head —
// first system and first user message — is used because clients resend the
// whole history every turn, so only the beginning stays constant across turns.
func sessionIdentity(r *http.Request, system, firstUser string) string {
	if hint := credential.SessionHint(map[string]string{
		"X-Session-Id":        r.Header.Get("X-Session-Id"),
		"X-Conversation-Id":   r.Header.Get("X-Conversation-Id"),
		"X-Client-Request-Id": r.Header.Get("X-Client-Request-Id"),
	}); hint != "" {
		return hint
	}
	if strings.TrimSpace(system) == "" && strings.TrimSpace(firstUser) == "" {
		return ""
	}
	return credential.Fingerprint(system, firstUser)
}

// withCredential picks a key for one request, runs it, and on a retryable
// upstream failure retries once on a different key. It is closure-based rather
// than bound to the Adapter interface because one of the three call sites
// (anthropicPassthrough) builds its own HTTP request and never touches an
// adapter; a closure covers all three without widening Adapter.
//
// It returns the upstream response for the caller to relay, the lease that
// served it, and an error only when no attempt produced a response at all. A
// retryable failure that exhausts the pool comes back as the last upstream
// response, so the client still sees the real status code and body.
func (s *Server) withCredential(ctx context.Context, p provider.Provider, session string, run func(key string) (*http.Response, error)) (*http.Response, credential.Lease, error) {
	var (
		prevResp  *http.Response
		prevLease credential.Lease
		lastErr   error
	)
	tried := make(map[string]struct{}, maxCredentialAttempts)

	for attempt := 0; attempt < maxCredentialAttempts; attempt++ {
		lease, err := s.credentials.Acquire(p.ID, p.CredentialMode, session)
		if err != nil {
			// A pool with a usable key still could not be reached; report what
			// the upstream last said rather than masking it with "no key".
			if prevResp != nil {
				return prevResp, prevLease, nil
			}
			return nil, credential.Lease{}, err
		}
		if _, repeated := tried[lease.CredentialID]; repeated {
			// No untried key remains: the pool has exactly one usable
			// credential. Hand back the previous response untouched instead of
			// retrying the same key and paying for the same failure twice.
			if prevResp != nil {
				return prevResp, prevLease, nil
			}
			return nil, credential.Lease{}, lastErr
		}
		tried[lease.CredentialID] = struct{}{}

		resp, err := run(lease.Secret())
		if err != nil {
			s.credentials.Report(lease.CredentialID, credential.Attempt{Err: err})
			lastErr = err
			continue
		}
		if !retryableStatus(resp.StatusCode) {
			if prevResp != nil {
				_ = prevResp.Body.Close()
			}
			s.credentials.Report(lease.CredentialID, credential.Attempt{StatusCode: resp.StatusCode})
			return resp, lease, nil
		}

		s.credentials.Report(lease.CredentialID, credential.Attempt{
			StatusCode: resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		})
		// Hold this response in case it is the pool's last word, releasing any
		// previous one so retries cannot leak connections.
		if prevResp != nil {
			_ = prevResp.Body.Close()
		}
		prevResp, prevLease = resp, lease
	}

	if prevResp != nil {
		return prevResp, prevLease, nil
	}
	return nil, credential.Lease{}, lastErr
}

// retryableStatus reports whether another key is worth trying. 401/403 mean this
// key is rejected, 429 means its quota window is closed, and 5xx may be
// key-specific on shared gateways. Other 4xx describe the request itself, so a
// different key would fail identically.
func retryableStatus(status int) bool {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden, status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	default:
		return false
	}
}

// parseRetryAfter reads the upstream's own cool-down hint. Only the delta-seconds
// form is honoured; an HTTP-date or junk value yields zero, which the pool
// replaces with its default rate-limit window.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return 0
	}
	window := time.Duration(seconds) * time.Second
	// Guard against an upstream asking for an absurdly long window, which would
	// effectively remove the key from the pool.
	if window > time.Hour {
		return time.Hour
	}
	return window
}

// firstMessageText returns the first message's content text for the given role,
// used to fingerprint the head of a conversation.
func firstMessageText(messages []Message, role string) string {
	for _, message := range messages {
		if strings.EqualFold(message.Role, role) {
			return string(message.Content)
		}
	}
	return ""
}

// firstAnthropicMessageText is the Anthropic-shape equivalent.
func firstAnthropicMessageText(messages []AnthropicMessage, role string) string {
	for _, message := range messages {
		if strings.EqualFold(message.Role, role) {
			return string(message.Content)
		}
	}
	return ""
}

// credentialError classifies a credential failure into the status, error kind
// and message the client sees. A pool with no usable key is a configuration
// problem (424) and names the provider so the operator knows which one to fix;
// anything else is an upstream transport failure.
func credentialError(err error, providerName string) (int, string, string) {
	if err == credential.ErrNoCredentials {
		return http.StatusFailedDependency, "provider_credentials_missing", "no API key configured for " + providerName
	}
	return http.StatusBadGateway, "upstream_error", err.Error()
}
