// Package stt transcribes Telegram voice messages (OGG/Opus) to text via
// Google Cloud Speech-to-Text, reusing the same service account credentials
// already required for Google Calendar.
package stt

import (
	"context"
	"fmt"
	"strings"

	speech "cloud.google.com/go/speech/apiv1"
	speechpb "cloud.google.com/go/speech/apiv1/speechpb"
	"google.golang.org/api/option"
)

// Client transcribes audio using Google Cloud Speech-to-Text.
type Client struct {
	speech       *speech.Client
	languageCode string
}

func NewClient(ctx context.Context, serviceAccountJSON, languageCode string) (*Client, error) {
	c, err := speech.NewClient(ctx, option.WithCredentialsJSON([]byte(serviceAccountJSON)))
	if err != nil {
		return nil, fmt.Errorf("create speech client: %w", err)
	}
	return &Client{speech: c, languageCode: languageCode}, nil
}

// Transcribe converts OGG/Opus audio bytes (Telegram's voice message format)
// into text.
func (c *Client) Transcribe(ctx context.Context, oggOpusAudio []byte) (string, error) {
	resp, err := c.speech.Recognize(ctx, &speechpb.RecognizeRequest{
		Config: &speechpb.RecognitionConfig{
			Encoding:        speechpb.RecognitionConfig_OGG_OPUS,
			SampleRateHertz: 48000,
			LanguageCode:    c.languageCode,
		},
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Content{Content: oggOpusAudio},
		},
	})
	if err != nil {
		return "", fmt.Errorf("recognize: %w", err)
	}

	var sb strings.Builder
	for _, result := range resp.Results {
		if len(result.Alternatives) == 0 {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(result.Alternatives[0].Transcript)
	}

	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("no speech recognized")
	}
	return text, nil
}
