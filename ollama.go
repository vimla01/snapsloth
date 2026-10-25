package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

var categories = []string{
	"upi_payment", "bank_or_otp", "college_notice", "timetable", "chat",
	"meme", "shopping", "code_or_error", "ticket_or_booking", "id_document",
	"social_post", "notes_or_study", "other",
}

var sensitiveTypes = []string{"otp", "bank_details", "upi_id", "aadhaar", "pan", "password", "phone", "address"}

// Analysis is what the model returns for one screenshot.
type Analysis struct {
	Category       string   `json:"category"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	Text           string   `json:"text"`
	Sensitive      bool     `json:"sensitive"`
	SensitiveTypes []string `json:"sensitive_types"`
}

// analysisSchema is passed as Ollama's `format`, which constrains decoding so
// the reply is always JSON in this shape.
var analysisSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"category":        map[string]any{"type": "string", "enum": categories},
		"title":           map[string]any{"type": "string"},
		"summary":         map[string]any{"type": "string"},
		"text":            map[string]any{"type": "string"},
		"sensitive":       map[string]any{"type": "boolean"},
		"sensitive_types": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": sensitiveTypes}},
	},
	"required": []string{"category", "title", "summary", "text", "sensitive", "sensitive_types"},
}

const analysisPrompt = `You are sorting a student's phone/laptop screenshots (India). For each screenshot, reply with JSON only.

category, pick exactly one:
- upi_payment: GPay/PhonePe/Paytm/BHIM payment, receipt, UPI request
- bank_or_otp: bank app, SMS with OTP, balance, transaction alert
- college_notice: circular, exam/fee notice, result, announcement
- timetable: class or exam schedule
- chat: WhatsApp/Instagram/Telegram conversation
- meme: joke, meme, funny image
- shopping: product page, cart, order, delivery status
- code_or_error: code, terminal, stack trace, error dialog
- ticket_or_booking: train/bus/flight/movie ticket, PNR, hotel booking
- id_document: Aadhaar, PAN, college ID, licence, any ID card
- social_post: tweet, Instagram/LinkedIn/Reddit post
- notes_or_study: notes, slides, textbook page, question paper
- other: anything else

title: short 3-6 word title.
summary: one line, what it is and why the person probably saved it.
text: the important readable text in the image, as plain text, at most about 50 words, most important first. Skip menus, buttons, tabs and status bars.
sensitive: true only if the image actually shows a real OTP, bank account or card number, UPI ID, Aadhaar or PAN number, password, phone number or home address. Ordinary numbers (process IDs, prices, dates, versions, stats) are not sensitive.
sensitive_types: which of those it shows (empty list if none).`

type OllamaClient struct {
	Host      string
	Model     string
	ImageSize int // longest side images are shrunk to before sending; 0 = as is
	HTTP      *http.Client
	noThink   bool // set once the server rejected the `think` field
}

func NewOllamaClient(host, model string) *OllamaClient {
	return &OllamaClient{
		Host:      strings.TrimRight(host, "/"),
		Model:     model,
		ImageSize: defaultImageSize,
		// CPU inference is slow and the first call also loads the model.
		HTTP: &http.Client{Timeout: 15 * time.Minute},
	}
}

// Check makes sure Ollama is up and the model is pulled, with an error that
// says exactly how to fix it.
func (c *OllamaClient) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Host+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("can't reach Ollama at %s (%v)\n\nIs it installed and running? Fix with:\n  curl -fsSL https://ollama.com/install.sh | sh\n  sudo systemctl start ollama     # or: ollama serve", c.Host, err)
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return fmt.Errorf("unexpected reply from Ollama at %s: %v", c.Host, err)
	}
	want := c.Model
	if !strings.Contains(want, ":") {
		want += ":latest"
	}
	for _, m := range tags.Models {
		if m.Name == want || m.Name == c.Model {
			return nil
		}
	}
	return fmt.Errorf("model %q isn't pulled yet. Fix with:\n  ollama pull %s", c.Model, c.Model)
}

type generateRequest struct {
	Model   string         `json:"model"`
	System  string         `json:"system,omitempty"`
	Prompt  string         `json:"prompt"`
	Images  []string       `json:"images"`
	Stream  bool           `json:"stream"`
	Format  any            `json:"format"`
	Think   *bool          `json:"think,omitempty"`
	Options map[string]any `json:"options"`
}

// Analyze sends one image to the model and returns its parsed answer.
func (c *OllamaClient) Analyze(ctx context.Context, image []byte) (*Analysis, error) {
	req := generateRequest{
		Model:   c.Model,
		// The instructions go in the system prompt, ahead of the image, so
		// Ollama can reuse them from cache instead of re-reading ~370 tokens
		// for every screenshot.
		System:  analysisPrompt,
		Prompt:  "Analyse this screenshot.",
		Images:  []string{base64.StdEncoding.EncodeToString(shrinkForModel(image, c.ImageSize))},
		Stream:  false,
		Format:  analysisSchema,
		Options: map[string]any{"temperature": 0.1, "num_predict": 768},
	}
	if !c.noThink {
		req.Think = new(false)
	}

	raw, err := c.generate(ctx, req)
	if err != nil && req.Think != nil && strings.Contains(strings.ToLower(err.Error()), "think") {
		c.noThink = true
		req.Think = nil
		raw, err = c.generate(ctx, req)
	}
	if err != nil {
		return nil, err
	}

	var a Analysis
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("model returned invalid JSON (output cut off?): %v", err)
	}
	if !slices.Contains(categories, a.Category) {
		a.Category = "other"
	}
	a.SensitiveTypes = slices.DeleteFunc(a.SensitiveTypes, func(t string) bool { return !slices.Contains(sensitiveTypes, t) })
	return &a, nil
}

func (c *OllamaClient) generate(ctx context.Context, body generateRequest) (string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Host+"/api/generate", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(respData, &out); err != nil {
		return "", fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(respData))
	}
	if out.Error != "" {
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("ollama: %s\n\nFix with:\n  ollama pull %s", out.Error, c.Model)
		}
		return "", errors.New("ollama: " + out.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama: HTTP %d", resp.StatusCode)
	}
	return out.Response, nil
}
