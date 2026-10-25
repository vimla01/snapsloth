package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeAadhaar appends a Verhoeff check digit to a made-up 11-digit prefix.
func fakeAadhaar(t *testing.T, prefix string) string {
	t.Helper()
	for d := range 10 {
		if s := prefix + strconv.Itoa(d); verhoeffValid(s) {
			return s
		}
	}
	t.Fatalf("no check digit for %s", prefix)
	return ""
}

func TestScrub(t *testing.T) {
	aadhaar := fakeAadhaar(t, "23456789012")
	spaced := aadhaar[:4] + " " + aadhaar[4:8] + " " + aadhaar[8:]

	tests := []struct {
		name     string
		in       string
		wantType string
		want     string // substring expected in the masked text
		raw      string // must not survive masking
	}{
		{"otp after", "482913 is your OTP for login. Do not share.", "otp", "XXXXXX is your OTP", "482913"},
		{"otp before", "Your verification code: 771204", "otp", "code: XXXXXX", "771204"},
		{"aadhaar spaced", "Aadhaar No: " + spaced, "aadhaar", "XXXX-XXXX-" + aadhaar[8:], spaced},
		{"aadhaar checksum only", "Number " + aadhaar + " here", "aadhaar", "XXXX-XXXX-" + aadhaar[8:], aadhaar},
		{"pan", "PAN: ABCDE1234F", "pan", "XXXXXXXX4F", "ABCDE1234F"},
		{"upi", "Paid to fakeuser@okaxis via UPI", "upi_id", "fa****@okaxis", "fakeuser@okaxis"},
		{"upi ends sentence", "UPI ID: 9990001111@paytm. Request money", "upi_id", "99****@paytm.", "9990001111@paytm"},
		{"card grouped", "Card 4111 1111 1111 1111 exp 12/30", "bank_details", "XXXX-XXXX-XXXX-1111", "4111 1111 1111 1111"},
		{"card plain", "card4111111111111111", "", "", ""}, // glued to a word: no \b, ignored
		{"account", "A/c no. 00112233445 credited", "bank_details", "XXXXXXX3445", "00112233445"},
		{"password", "wifi password: chai4ever!", "password", "password: ********", "chai4ever!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, findings := Scrub(tt.in)
			if tt.wantType == "" {
				if len(findings) != 0 {
					t.Fatalf("Scrub(%q) found %v, want nothing", tt.in, findings)
				}
				return
			}
			if !slices.Contains(FindingTypes(findings), tt.wantType) {
				t.Errorf("Scrub(%q) types = %v, want %s", tt.in, FindingTypes(findings), tt.wantType)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("Scrub(%q) = %q, want it to contain %q", tt.in, got, tt.want)
			}
			if strings.Contains(got, tt.raw) {
				t.Errorf("Scrub(%q) = %q still contains raw value", tt.in, got)
			}
		})
	}
}

func TestScrubLeavesHarmlessTextAlone(t *testing.T) {
	aadhaar := fakeAadhaar(t, "23456789012")
	// Flip the check digit: Verhoeff catches every single-digit change.
	badLast := (int(aadhaar[11]-'0') + 1) % 10
	upiRef := aadhaar[:11] + strconv.Itoa(badLast)

	for _, in := range []string{
		"Mail me at someone@example.com",
		"Pune, PIN code 411001",
		"UPI Ref No " + upiRef,
		"Forgot password?",
		"Order #123456 shipped", // 6 digits but no OTP context
		"Meeting at 10:30 in room 204",
	} {
		if got, findings := Scrub(in); len(findings) != 0 || got != in {
			t.Errorf("Scrub(%q) = %q, %v; want unchanged", in, got, findings)
		}
	}
}

func TestScrubMultipleAndTypes(t *testing.T) {
	in := "OTP 123456 for card 4111-1111-1111-1111. Card OTP valid 10 min."
	got, findings := Scrub(in)
	want := "OTP XXXXXX for card XXXX-XXXX-XXXX-1111. Card OTP valid 10 min."
	if got != want {
		t.Errorf("Scrub = %q, want %q", got, want)
	}
	if types := FindingTypes(findings); !slices.Equal(types, []string{"otp", "bank_details"}) {
		t.Errorf("FindingTypes = %v", types)
	}
}

func TestChecksums(t *testing.T) {
	if !luhnValid("4111111111111111") || luhnValid("4111111111111112") {
		t.Error("luhnValid wrong on test card")
	}
	if !verhoeffValid("2363") || verhoeffValid("2364") { // textbook Verhoeff example
		t.Error("verhoeffValid wrong on textbook example")
	}
}

func TestMaskHelpers(t *testing.T) {
	if got := maskGrouped("234567890123"); got != "XXXX-XXXX-0123" {
		t.Errorf("maskGrouped = %q", got)
	}
	if got := maskKeepLast("98-7654-3210", 4); got != "XX-XXXX-3210" {
		t.Errorf("maskKeepLast = %q", got)
	}
}

func TestIDCardKind(t *testing.T) {
	tests := map[string]string{
		"भारत सरकार GOVERNMENT OF INDIA Test Kumar DOB: 01-01-2000 MALE आधार - आम आदमी का अधिकार": "aadhaar",
		"Unique Identification Authority of India. Year of Birth 1999":                            "aadhaar",
		"INCOME TAX DEPARTMENT GOVT. OF INDIA Permanent Account Number Card":                      "pan",
		"Aadhaar Seva Kendra timings: 9 AM to 5 PM":                                               "", // talks about Aadhaar, isn't a card
		"Meeting notes, DOB field validation bug":                                                 "",
	}
	for in, want := range tests {
		if got := IDCardKind(in); got != want {
			t.Errorf("IDCardKind(%q) = %q, want %q", in, got, want)
		}
	}
}
