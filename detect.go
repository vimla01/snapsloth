package main

import (
	"regexp"
	"slices"
	"strings"
)

// Finding is one sensitive value spotted in text. The raw value is never kept;
// only the masked form ever leaves this file.
type Finding struct {
	Type   string // same vocabulary as the model's sensitive_types
	Masked string
}

type span struct {
	start, end int
	kind       string
	masked     string
}

var (
	reCardPlain   = regexp.MustCompile(`\b\d{13,19}\b`)
	reCardGrouped = regexp.MustCompile(`\b\d{4}(?:[ -]\d{4}){3}(?:[ -]?\d{1,3})?\b`)
	reAccount     = regexp.MustCompile(`\b\d{9,18}\b`)
	reAadhaar     = regexp.MustCompile(`\b[2-9]\d{3}[ -]?\d{4}[ -]?\d{4}\b`)
	reOTP         = regexp.MustCompile(`\b\d{6}\b`)
	rePAN         = regexp.MustCompile(`\b[A-Z]{5}\d{4}[A-Z]\b`)
	reUPI         = regexp.MustCompile(`\b[A-Za-z0-9._-]{2,64}@[A-Za-z][A-Za-z0-9]{1,31}\b`)
	rePassword    = regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|passcode|mpin)\s*[:=]\s*(\S{3,})`)

	ctxOTP     = regexp.MustCompile(`(?i)otp|one[ -]?time|verification|security code|login code|2fa`)
	ctxAccount = regexp.MustCompile(`(?i)a/c|\bacc(?:ount|t)?\b|account`)
	ctxAadhaar = regexp.MustCompile(`(?i)aadhaa?r|uidai|\bvid\b|आधार`)
)

// Scrub finds sensitive values in text and returns the text with each of them
// masked, plus what was found. It is a regex backup to the model: dumb, but it
// never forgets to look.
func Scrub(text string) (string, []Finding) {
	spans := detectSpans(text)
	if len(spans) == 0 {
		return text, nil
	}
	var b strings.Builder
	findings := make([]Finding, 0, len(spans))
	last := 0
	for _, s := range spans {
		b.WriteString(text[last:s.start])
		b.WriteString(s.masked)
		last = s.end
		findings = append(findings, Finding{Type: s.kind, Masked: s.masked})
	}
	b.WriteString(text[last:])
	return b.String(), findings
}

// FindingTypes returns the distinct types in findings, in first-seen order.
func FindingTypes(findings []Finding) []string {
	var types []string
	for _, f := range findings {
		if !slices.Contains(types, f.Type) {
			types = append(types, f.Type)
		}
	}
	return types
}

// detectSpans runs every detector in priority order. A later detector can't
// claim text an earlier one already took, so a card number is never also
// reported as an Aadhaar.
func detectSpans(text string) []span {
	var accepted []span
	add := func(s span) {
		for _, a := range accepted {
			if s.start < a.end && a.start < s.end {
				return
			}
		}
		accepted = append(accepted, s)
	}

	for _, re := range []*regexp.Regexp{reCardGrouped, reCardPlain} {
		for _, m := range re.FindAllStringIndex(text, -1) {
			if d := digitsOnly(text[m[0]:m[1]]); luhnValid(d) {
				add(span{m[0], m[1], "bank_details", maskGrouped(d)})
			}
		}
	}
	for _, m := range reAccount.FindAllStringIndex(text, -1) {
		if near(text, m[0], m[1], 30, 0, ctxAccount) {
			add(span{m[0], m[1], "bank_details", maskKeepLast(text[m[0]:m[1]], 4)})
		}
	}
	for _, m := range reAadhaar.FindAllStringIndex(text, -1) {
		d := digitsOnly(text[m[0]:m[1]])
		// UPI ref numbers are also 12 digits; the checksum or a nearby
		// "Aadhaar" keeps most of those out.
		if verhoeffValid(d) || near(text, m[0], m[1], 60, 20, ctxAadhaar) {
			add(span{m[0], m[1], "aadhaar", maskGrouped(d)})
		}
	}
	for _, m := range reOTP.FindAllStringIndex(text, -1) {
		if near(text, m[0], m[1], 50, 50, ctxOTP) {
			add(span{m[0], m[1], "otp", strings.Repeat("X", m[1]-m[0])})
		}
	}
	for _, m := range rePAN.FindAllStringIndex(text, -1) {
		v := text[m[0]:m[1]]
		add(span{m[0], m[1], "pan", "XXXXXXXX" + v[8:]})
	}
	for _, m := range reUPI.FindAllStringIndex(text, -1) {
		// name@gmail.com is an email, not a UPI ID.
		if m[1] < len(text) && text[m[1]] == '.' {
			continue
		}
		v := text[m[0]:m[1]]
		at := strings.IndexByte(v, '@')
		add(span{m[0], m[1], "upi_id", v[:min(2, at)] + "****" + v[at:]})
	}
	for _, m := range rePassword.FindAllStringSubmatchIndex(text, -1) {
		// Mask only the value, and with a fixed length so it doesn't leak that either.
		add(span{m[2], m[3], "password", "********"})
	}

	slices.SortFunc(accepted, func(a, b span) int { return a.start - b.start })
	return accepted
}

// near reports whether re matches within `before` bytes before or `after`
// bytes after text[start:end].
func near(text string, start, end, before, after int, re *regexp.Regexp) bool {
	return re.MatchString(text[max(0, start-before):min(len(text), end+after)])
}

func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// maskKeepLast replaces every digit except the last n with X, keeping separators.
func maskKeepLast(s string, n int) string {
	total := len(digitsOnly(s))
	seen := 0
	return strings.Map(func(r rune) rune {
		if r < '0' || r > '9' {
			return r
		}
		seen++
		if seen <= total-n {
			return 'X'
		}
		return r
	}, s)
}

// maskGrouped turns "234512345678" into "XXXX-XXXX-5678".
func maskGrouped(digits string) string {
	m := maskKeepLast(digits, 4)
	var parts []string
	for len(m) > 4 {
		parts = append(parts, m[:4])
		m = m[4:]
	}
	return strings.Join(append(parts, m), "-")
}

func luhnValid(digits string) bool {
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return len(digits) > 0 && sum%10 == 0
}

// Verhoeff checksum, which every real Aadhaar number satisfies.
var (
	verhoeffD = [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
		{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
		{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
		{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
		{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
		{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
		{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
		{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
		{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
	}
	verhoeffP = [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
		{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
		{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
		{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
		{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
		{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
	}
)

func verhoeffValid(digits string) bool {
	c := 0
	for i := range len(digits) {
		d := int(digits[len(digits)-1-i] - '0')
		c = verhoeffD[c][verhoeffP[i%8][d]]
	}
	return len(digits) > 0 && c == 0
}
