package storage

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strconv"
	"strings"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// parsePlistDict reads the top-level dictionary of an XML property list,
// as diskutil -plist prints it: strings, integers, reals and booleans are
// kept; nested arrays and dictionaries are skipped. Nil if it is not one.
func parsePlistDict(b []byte) map[string]any {
	if len(b) == 0 {
		return nil
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	out := map[string]any{}
	depth, key, inTop := 0, "", false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if name == "plist" {
				continue
			}
			depth++
			if name == "dict" && depth == 1 {
				inTop = true
				continue
			}
			if !inTop || depth != 2 {
				if name == "dict" || name == "array" {
					continue // nested: walked over, not kept
				}
				var skip string
				dec.DecodeElement(&skip, &t)
				depth--
				continue
			}
			switch name {
			case "true", "false":
				if key != "" {
					out[key] = name == "true"
				}
				dec.Skip()
				depth--
				key = ""
			case "dict", "array":
				key = "" // nested value: depth counts down at its end
			default:
				var text string
				dec.DecodeElement(&text, &t)
				depth--
				text = strings.TrimSpace(text)
				switch name {
				case "key":
					key = text
					continue
				case "integer":
					if n, err := strconv.ParseInt(text, 10, 64); err == nil && key != "" {
						out[key] = n
					}
				case "real":
					if f, err := strconv.ParseFloat(text, 64); err == nil && key != "" {
						out[key] = f
					}
				default:
					if key != "" {
						out[key] = text
					}
				}
				key = ""
			}
		case xml.EndElement:
			if t.Name.Local != "plist" {
				depth--
			}
		}
	}
	if !inTop {
		return nil
	}
	return out
}
