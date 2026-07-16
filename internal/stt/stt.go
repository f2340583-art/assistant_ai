// Package stt transcribes Telegram voice messages (OGG/Opus) to text via
// the Gemini API's native audio understanding. Chosen over Google Cloud
// Speech-to-Text because Uzbek is a low-resource language for classic ASR
// acoustic models (poor handling of dialect, informal speech, and
// code-switching with Russian) — Gemini's broad multilingual pretraining
// and contextual understanding transcribes conversational Uzbek far more
// reliably, and only needs an API key (no service account/billing setup).
package stt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const transcribeInstruction = `Transcribe this Uzbek voice message exactly as spoken. Output ONLY the raw transcript in Uzbek Latin script, with no commentary, labels, or translation. If the speaker code-switches into Russian, keep those words as spoken (Cyrillic or transliterated, whichever they used). If the audio contains no discernible speech, output exactly: NO_SPEECH`

// Client transcribes audio using the Gemini API's generateContent endpoint
// with inline audio input.
type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

func NewClient(apiKey, model string) *Client {
	return &Client{apiKey: apiKey, model: model, http: &http.Client{}}
}

type generateContentRequest struct {
	Contents []content `json:"contents"`
}

type content struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type generateContentResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

// Transcribe converts OGG/Opus audio bytes (Telegram's voice message format)
// into text.
func (c *Client) Transcribe(ctx context.Context, oggOpusAudio []byte) (string, error) {
	reqBody := generateContentRequest{
		Contents: []content{{
			Parts: []part{
				{Text: transcribeInstruction},
				{InlineData: &inlineData{
					MimeType: "audio/ogg",
					Data:     base64.StdEncoding.EncodeToString(oggOpusAudio),
				}},
			},
		}},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", c.model, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini request failed: status %d: %s", resp.StatusCode, respBody)
	}

	var parsed generateContentResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no speech recognized")
	}

	text := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
	if text == "" || text == "NO_SPEECH" {
		return "", fmt.Errorf("no speech recognized")
	}
	return text, nil
}
