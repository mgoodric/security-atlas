package demoseed

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// DemoPasswordLength is the minimum number of characters the seeder
// emits for the one-time-printed password. AC-12 requires at least 16.
// Decision D4: 20 chars chosen — comfortably above the floor; survives
// one-char-eyeballing transcription errors during a live demo.
const DemoPasswordLength = 20

// passwordAlphabet is the deterministic alphabet used for the demo
// user's one-time-printed password. Ambiguous glyphs (`0`/`O`, `1`/`I`/`l`)
// are excluded so a live-demo viewer reading the password off a screen
// has no parsing trouble.
//
// Each character class is present to satisfy "mixed alphabet" in AC-12.
// The min-class-counts function enforces at least 1 from each category.
const (
	passwordLowers  = "abcdefghjkmnpqrstuvwxyz" // no i, l, o
	passwordUppers  = "ABCDEFGHJKMNPQRSTUVWXYZ" // no I, L, O
	passwordDigits  = "23456789"                // no 0, 1
	passwordSymbols = "!@#$%^&*-_+=?"           // no quote-types or shell-meta-y chars
)

// GenerateDemoPassword returns a fresh password of DemoPasswordLength
// chars sampled uniformly from passwordAlphabet using crypto/rand. The
// result is guaranteed to contain at least one of each character class.
//
// Decision D4 (full rationale in docs/audit-log/205-demo-seed-data-decisions.md):
//
//   - 20 chars (>16 floor) — survives transcription; comfortable above
//     the floor without being unwieldy to type during a demo handoff.
//   - crypto/rand — the platform's only cryptographic randomness source;
//     internal/auth/password and internal/auth/keystore both use it.
//   - Class enforcement (at least one lower / upper / digit / symbol) —
//     keeps the password agreement with realistic enterprise password
//     policies and avoids the "all lowercase" floor case.
//   - No ambiguous chars — explicit anti-criterion AC-12 ("no O/0/I/l/1").
//
// Never log or persist the returned string. The CLI prints it once to
// stdout and the caller hashes it with argon2id before INSERTing the
// local_credentials row (P0-A2).
func GenerateDemoPassword() (string, error) {
	// Sample N-4 chars uniformly from the union alphabet, then append
	// one mandatory char from each class, then Fisher-Yates shuffle so
	// the mandatory positions are not predictable.
	alphabet := passwordLowers + passwordUppers + passwordDigits + passwordSymbols
	if len(alphabet) == 0 {
		return "", fmt.Errorf("demoseed: empty password alphabet")
	}
	if DemoPasswordLength < 16 {
		// Defense-in-depth — never produce a short password even if a
		// future caller patches the constant down.
		return "", fmt.Errorf("demoseed: DemoPasswordLength must be >= 16")
	}

	// 4 mandatory class chars + (length-4) uniformly-random chars.
	mandatory := []string{
		passwordLowers,
		passwordUppers,
		passwordDigits,
		passwordSymbols,
	}
	out := make([]byte, 0, DemoPasswordLength)
	for _, class := range mandatory {
		c, err := pickOne(class)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for i := 0; i < DemoPasswordLength-len(mandatory); i++ {
		c, err := pickOne(alphabet)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}

	// Fisher-Yates shuffle so mandatory class chars are not always at
	// positions 0-3. Uses crypto/rand to drive the swaps.
	for i := len(out) - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", fmt.Errorf("demoseed: shuffle: %w", err)
		}
		j := jBig.Int64()
		out[i], out[j] = out[j], out[i]
	}
	return string(out), nil
}

// pickOne returns one uniformly-sampled character from src.
func pickOne(src string) (byte, error) {
	if len(src) == 0 {
		return 0, fmt.Errorf("demoseed: empty source alphabet")
	}
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(src))))
	if err != nil {
		return 0, fmt.Errorf("demoseed: random pick: %w", err)
	}
	return src[nBig.Int64()], nil
}
