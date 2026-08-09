// Package label turns per-row identities into labels that are distinct
// within one listing.
//
// Naming processes after their bundle made the table readable and introduced
// a new problem: one app can own several listeners, so a real machine shows
// three rows all reading "Antigravity IDE" with nothing to tell them apart.
// The fix is not to make every identity globally unique — "OneDrive" is the
// right label when OneDrive appears once — but to add a distinguishing suffix
// only where a listing actually has a collision.
//
// Disambiguation is therefore a property of the LIST, not of a row, which is
// why it lives here rather than in intel: intel classifies one listener at a
// time and cannot know what else is on screen.
package label

import "strings"

// Item is one row's naming inputs: the identity intel derived, and the helper
// binary behind it (lsof's command name).
type Item struct {
	Identity string
	Helper   string
}

// Disambiguate returns one label per item, in the same order.
//
// An identity that appears once is returned untouched. When several rows share
// an identity AND their helpers differ, each gets "Identity · helper". When
// the helpers are identical too, the rows are genuinely the same kind of
// process and the label stays clean — the port column already tells them
// apart, and "Antigravity IDE · language_server" twice over would add width
// without adding information.
func Disambiguate(items []Item) []string {
	helpersByIdentity := map[string]map[string]bool{}
	for _, it := range items {
		set := helpersByIdentity[it.Identity]
		if set == nil {
			set = map[string]bool{}
			helpersByIdentity[it.Identity] = set
		}
		set[shortHelper(it.Identity, it.Helper)] = true
	}

	out := make([]string, len(items))
	for i, it := range items {
		helper := shortHelper(it.Identity, it.Helper)
		if helper != "" && len(helpersByIdentity[it.Identity]) > 1 {
			out[i] = it.Identity + " · " + helper
			continue
		}
		out[i] = it.Identity
	}
	return out
}

// noiseSuffixes are platform and architecture tags that pad a binary name
// without distinguishing it from its siblings — "language_server_macos_arm"
// and "language_server" are the same thing, and the column space matters more
// than the tag does.
var noiseSuffixes = []string{
	"_macos", "_darwin", "_linux", "_windows",
	"_arm64", "_aarch64", "_amd64", "_x86_64", "_x64", "_arm", "_x86",
	"-macos", "-darwin", "-linux", "-windows",
	"-arm64", "-aarch64", "-amd64", "-x86_64", "-x64", "-arm", "-x86",
}

// shortHelper trims a helper binary down to the part that actually
// distinguishes it, and returns "" when it adds nothing (empty, or just the
// identity again).
func shortHelper(identity, helper string) string {
	h := strings.TrimSpace(helper)
	if h == "" || strings.EqualFold(h, identity) {
		return ""
	}
	// Repeatedly, because names stack them: "…_macos_arm" -> "…_macos" -> "…".
	for trimmed := true; trimmed; {
		trimmed = false
		for _, suffix := range noiseSuffixes {
			if len(h) > len(suffix) && strings.HasSuffix(strings.ToLower(h), suffix) {
				h = h[:len(h)-len(suffix)]
				trimmed = true
			}
		}
	}
	if h == "" || strings.EqualFold(h, identity) {
		return ""
	}
	return h
}
