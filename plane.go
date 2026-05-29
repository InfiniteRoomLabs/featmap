package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/microcosm-cc/bluemonday"
)

// Plane integration: REST client, credential crypto, and the value-typed enums
// backing plane_comment_origin / plane_sync_status. The sync engine itself lives
// on the service (service.go); this file is the Plane-facing edge + helpers.

// CommentOrigin mirrors the plane_comment_origin Postgres enum.
type CommentOrigin string

const (
	OriginFeatmap CommentOrigin = "featmap"
	OriginPlane   CommentOrigin = "plane"
)

func (o CommentOrigin) Valid() bool { return o == OriginFeatmap || o == OriginPlane }

// SyncStatus mirrors the plane_sync_status Postgres enum.
type SyncStatus string

const (
	StatusOK      SyncStatus = "ok"
	StatusError   SyncStatus = "error"
	StatusPending SyncStatus = "pending"
)

func (s SyncStatus) Valid() bool {
	return s == StatusOK || s == StatusError || s == StatusPending
}

// decodeKey turns the base64 conf key into a 32-byte AES-256 key.
func decodeKey(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, errors.New("planeEncryptionKey not configured")
	}
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("planeEncryptionKey is not valid base64")
	}
	if len(k) != 32 {
		return nil, errors.New("planeEncryptionKey must decode to 32 bytes (AES-256)")
	}
	return k, nil
}

// encryptPlaneKey AES-256-GCM encrypts a Plane API key. Returns ciphertext +
// the per-record nonce. The encryption key is the base64 conf value.
func encryptPlaneKey(keyB64, plaintext string) (ciphertext, nonce []byte, err error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, nil
}

// decryptPlaneKey reverses encryptPlaneKey.
func decryptPlaneKey(keyB64 string, ciphertext, nonce []byte) (string, error) {
	key, err := decodeKey(keyB64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("failed to decrypt plane key (wrong key or corrupt data)")
	}
	return string(plaintext), nil
}

// planeHTMLSanitizer strips ALL tags from Plane comment HTML, leaving safe text.
// Plane comment authors are untrusted (a different, larger population than the
// admin who configured the connection), so their comment_html must never be
// stored raw: it is served verbatim by get_feature/get_board/query_board and
// would be a stored-XSS payload for any consumer that renders HTML. StrictPolicy
// removes every tag (so no <script>, no event handlers, no javascript: URIs
// survive) and the result is plain text -- which is also the right shape for the
// markdown-rendered `post` field (react-markdown escapes HTML anyway, so raw tags
// would render as ugly literals; stripping them renders cleaner). Full md<->html
// fidelity is deferred (icebox ICE-034).
var planeHTMLSanitizer = bluemonday.StrictPolicy()

func sanitizePlaneCommentHTML(html string) string {
	return planeHTMLSanitizer.Sanitize(html)
}

// PlaneComment is the subset of a Plane work-item comment we use.
type PlaneComment struct {
	ID          string    `json:"id"`
	CommentHTML string    `json:"comment_html"`
	UpdatedAt   time.Time `json:"updated_at"`
	Actor       string    `json:"actor"`
}

// PlaneClient is a thin Plane REST client. Auth via the X-API-Key header.
type PlaneClient struct {
	BaseURL        string // e.g. https://api.plane.so
	APIKey         string
	PlaneWorkspace string // workspace slug
	HTTP           *http.Client
	// AllowPrivate disables the SSRF guard's private/loopback/link-local
	// rejection. Default false. Set only from the operator opt-in conf flag
	// planeAllowPrivateHosts, for legitimately self-hosted Plane on an internal
	// address. Never expose this to per-request/user input.
	AllowPrivate bool
}

// isBlockedHostIP reports whether an IP must be refused as an SSRF target.
// Uses stdlib classification: loopback (127/8, ::1), unspecified (0.0.0.0, ::),
// RFC1918 + ULA fc00::/7 (IsPrivate), and link-local (169.254/16 incl. the
// 169.254.169.254 cloud-metadata address, and fe80::/10). Multicast too.
func isBlockedHostIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// validatePlaneBaseURL rejects URLs that could drive SSRF. It requires an
// http/https scheme and a host, resolves the host, and (unless allowPrivate)
// refuses any address that resolves to a loopback/private/link-local range so a
// workspace member cannot point base_url at cloud metadata or internal services.
// Called both on write (SetPlaneConnection) and before every request (do), so a
// DNS-rebinding flip between persist and use is caught at request time.
func validatePlaneBaseURL(rawURL string, allowPrivate bool) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid Plane base URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("Plane base URL must use http or https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("Plane base URL has no host")
	}
	if allowPrivate {
		return nil
	}
	// If the host is a literal IP, classify it directly; otherwise resolve.
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedHostIP(ip) {
			return fmt.Errorf("Plane base URL host %s is a private/loopback/link-local address (set planeAllowPrivateHosts to allow)", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve Plane base URL host %q: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedHostIP(ip) {
			return fmt.Errorf("Plane base URL host %s resolves to a private/loopback/link-local address %s (set planeAllowPrivateHosts to allow)", host, ip)
		}
	}
	return nil
}

// ssrfGuardControl is a net.Dialer.Control hook that rejects a connection whose
// RESOLVED address is private/loopback/link-local. Because Control runs after DNS
// resolution and immediately before connect, it closes the validate-then-dial
// TOCTOU/DNS-rebinding gap: even if a hostname resolved to a public IP at
// validation time and flips to 169.254.169.254 (or 127.0.0.1, 10/8, ...) by the
// time we dial, the dial itself is refused. It fires for the initial request AND
// every redirect hop (same Transport), so a 302 to an internal IP is blocked too.
func ssrfGuardControl(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("ssrf guard: cannot parse dial address %q: %w", address, err)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("ssrf guard: dial address %q is not an IP", host)
		}
		if isBlockedHostIP(ip) {
			return fmt.Errorf("ssrf guard: refusing to connect to private/loopback/link-local address %s", ip)
		}
		return nil
	}
}

func (c *PlaneClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   ssrfGuardControl(c.AllowPrivate),
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		// Belt-and-suspenders: also re-validate the redirect target URL's host up
		// front (the dialer Control is the authoritative IP-level enforcement).
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return validatePlaneBaseURL(req.URL.Scheme+"://"+req.URL.Host, c.AllowPrivate)
		},
	}
}

func (c *PlaneClient) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	// Re-validate at request time (defends against DNS rebinding / a base_url
	// that resolved public on write but flips to an internal IP later).
	if err := validatePlaneBaseURL(c.BaseURL, c.AllowPrivate); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient().Do(req)
}

// TestConnection calls GET /api/v1/users/me/ (SYNC-001).
func (c *PlaneClient) TestConnection(ctx context.Context) error {
	resp, err := c.do(ctx, "GET", "/api/v1/users/me/", nil)
	if err != nil {
		return fmt.Errorf("plane unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return errors.New("plane rejected the API key (401)")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("plane returned status %d", resp.StatusCode)
	}
	return nil
}

type planeCommentList struct {
	Results         []PlaneComment `json:"results"`
	NextCursor      string         `json:"next_cursor"`
	NextPageResults bool           `json:"next_page_results"`
}

func (c *PlaneClient) commentsPath(planeProjectID, workItemID string) string {
	return fmt.Sprintf("/api/v1/workspaces/%s/projects/%s/work-items/%s/comments/",
		url.PathEscape(c.PlaneWorkspace), url.PathEscape(planeProjectID), url.PathEscape(workItemID))
}

// maxCommentPages bounds the cursor-pagination loop in ListComments so a
// misbehaving Plane server that returns next_page_results=true forever cannot
// spin the calling goroutine indefinitely.
const maxCommentPages = 500

// ListComments fetches all comments for a work item, following cursor pages.
func (c *PlaneClient) ListComments(ctx context.Context, planeProjectID, workItemID string) ([]PlaneComment, error) {
	var all []PlaneComment
	cursor := ""
	for pages := 0; ; pages++ {
		if pages >= maxCommentPages {
			return nil, fmt.Errorf("plane comment pagination exceeded %d pages", maxCommentPages)
		}
		p := c.commentsPath(planeProjectID, workItemID) + "?per_page=100"
		if cursor != "" {
			p += "&cursor=" + url.QueryEscape(cursor)
		}
		resp, err := c.do(ctx, "GET", p, nil)
		if err != nil {
			return nil, fmt.Errorf("plane unreachable: %w", err)
		}
		if resp.StatusCode == 429 {
			resp.Body.Close()
			return nil, c.rateLimited(resp)
		}
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("plane list comments status %d", resp.StatusCode)
		}
		var page planeCommentList
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		all = append(all, page.Results...)
		if !page.NextPageResults || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// CreateComment posts a comment (comment_html) and returns the created comment.
func (c *PlaneClient) CreateComment(ctx context.Context, planeProjectID, workItemID, html string) (*PlaneComment, error) {
	payload, _ := json.Marshal(map[string]string{"comment_html": html})
	resp, err := c.do(ctx, "POST", c.commentsPath(planeProjectID, workItemID), strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("plane unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		return nil, c.rateLimited(resp)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("plane create comment status %d", resp.StatusCode)
	}
	var cm PlaneComment
	if err := json.NewDecoder(resp.Body).Decode(&cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

func (c *PlaneClient) rateLimited(resp *http.Response) error {
	reset := resp.Header.Get("X-RateLimit-Reset")
	if reset != "" {
		if epoch, err := strconv.ParseInt(reset, 10, 64); err == nil {
			return fmt.Errorf("plane rate limited; resets at %s", time.Unix(epoch, 0).UTC().Format(time.RFC3339))
		}
	}
	return errors.New("plane rate limited (429)")
}
