package web

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"
)

const maxLogoBytes = 512 * 1024

// Logo is a validated in-memory product logo. Its data remains private so callers
// cannot alter it after validation.
type Logo struct {
	data        []byte
	contentType string
}

// LoadLogo validates a configured raster or SVG logo before any network transition.
func LoadLogo(path string) (*Logo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open branding logo %q: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read branding logo %q: %w", path, err)
	}
	if len(data) == 0 {
		return nil, errors.New("branding logo must not be empty")
	}
	if len(data) > maxLogoBytes {
		return nil, fmt.Errorf("branding logo exceeds the %d-byte limit", maxLogoBytes)
	}

	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg":
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("decode branding logo: %w", err)
		}
	case "text/xml; charset=utf-8", "text/plain; charset=utf-8", "application/xml":
		if err := validateSVG(data); err != nil {
			return nil, fmt.Errorf("validate branding logo: %w", err)
		}
		contentType = "image/svg+xml"
	default:
		return nil, fmt.Errorf("branding logo has unsupported media type %q", contentType)
	}
	return &Logo{data: append([]byte(nil), data...), contentType: contentType}, nil
}

func validateSVG(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	rootSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode SVG XML: %w", err)
		}
		switch value := token.(type) {
		case xml.Directive:
			return errors.New("SVG directives are not allowed")
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if !rootSeen {
				if name != "svg" {
					return errors.New("SVG root element is required")
				}
				rootSeen = true
			}
			switch name {
			case "script", "style", "foreignobject", "iframe", "object", "embed", "audio", "video":
				return fmt.Errorf("SVG element %q is not allowed", value.Name.Local)
			}
			for _, attribute := range value.Attr {
				attributeName := strings.ToLower(attribute.Name.Local)
				attributeValue := strings.TrimSpace(attribute.Value)
				if strings.HasPrefix(attributeName, "on") {
					return fmt.Errorf("SVG event attribute %q is not allowed", attribute.Name.Local)
				}
				if (attributeName == "href" || attributeName == "src") &&
					attributeValue != "" && !strings.HasPrefix(attributeValue, "#") {
					return fmt.Errorf("external SVG reference %q is not allowed", attribute.Value)
				}
				if attributeName == "style" && strings.Contains(strings.ToLower(attributeValue), "url(") {
					return errors.New("SVG style URLs are not allowed")
				}
			}
		}
	}
	if !rootSeen {
		return errors.New("SVG root element is required")
	}
	return nil
}
