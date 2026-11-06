package tools

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// TextMaximumBytes bounds both UTF-8 input and the complete returned output.
const TextMaximumBytes = 1_048_576

const textMaximumJSONDepth = 128

var (
	ErrTextInputTooLarge          = errors.New("text exceeds the 1 MiB input limit")
	ErrTextOutputTooLarge         = errors.New("result exceeds the 1 MiB output limit")
	ErrTextInvalidUTF8            = errors.New("text is not valid UTF-8")
	ErrTextInvalidJSON            = errors.New("text is not valid JSON")
	ErrTextJSONNestingTooDeep     = errors.New("JSON exceeds the supported nesting depth of 128")
	ErrTextInvalidBase64          = errors.New("text is not valid canonical Base64")
	ErrTextInvalidPercentEncoding = errors.New("text contains invalid percent-encoding")
	ErrTextInvalidEpoch           = errors.New("enter whole Unix seconds between years 0001 and 9999")
	ErrTextRandomGeneration       = errors.New("secure random generation failed")
	ErrTextUnsupportedOperation   = errors.New("unsupported text operation")
)

// TextTransformer implements the portable text tools without a clipboard, shell,
// network, or file writes. Optional sources make clock/random failures testable;
// a zero value uses the system clock and crypto/rand.
type TextTransformer struct {
	Now    func() time.Time
	Random io.Reader
}

// ReadTextInput reads at most the input limit plus one byte and validates UTF-8.
// Callers should finish the transform before writing anything to stdout.
func ReadTextInput(reader io.Reader) ([]byte, error) {
	input, err := io.ReadAll(io.LimitReader(reader, TextMaximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read text input: %w", err)
	}
	if err := validateTextInput(input); err != nil {
		return nil, err
	}
	return input, nil
}

// Transform returns the entire bounded result, or nil on failure. Scalar values
// and reports include a final LF. Content transforms preserve their own exact
// content; JSON, Base64 encoding and the sample text include their native LF.
func (transformer TextTransformer) Transform(operation string, input []byte) ([]byte, error) {
	if err := validateTextInput(input); err != nil {
		return nil, err
	}
	var output []byte
	var err error
	switch operation {
	case "uuid":
		value, randomErr := transformer.randomBytes(16)
		if randomErr != nil {
			return nil, randomErr
		}
		value[6] = (value[6] & 0x0f) | 0x40
		value[8] = (value[8] & 0x3f) | 0x80
		output = fmt.Appendf(nil, "%X-%X-%X-%X-%X\n", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
	case "json":
		output, err = prettyTextJSON(input)
	case "epoch":
		now := transformer.Now
		if now == nil {
			now = time.Now
		}
		seconds := now().Unix()
		if !validTextEpoch(seconds) {
			return nil, ErrTextInvalidEpoch
		}
		output = strconv.AppendInt(nil, seconds, 10)
		output = append(output, '\n')
	case "epoch-to-date":
		seconds, parseErr := strconv.ParseInt(strings.TrimSpace(string(input)), 10, 64)
		if parseErr != nil || !validTextEpoch(seconds) {
			return nil, ErrTextInvalidEpoch
		}
		output = time.Unix(seconds, 0).UTC().AppendFormat(nil, time.RFC3339)
		output = append(output, '\n')
	case "plain":
		output = append([]byte(nil), input...)
	case "base64-encode":
		length := base64.StdEncoding.EncodedLen(len(input))
		if length+1 > TextMaximumBytes {
			return nil, ErrTextOutputTooLarge
		}
		output = make([]byte, length+1)
		base64.StdEncoding.Encode(output[:length], input)
		output[length] = '\n'
	case "base64-decode":
		output, err = decodeTextBase64(input)
	case "url-encode":
		output, err = encodeTextURL(strings.TrimSpace(string(input)))
	case "url-decode":
		var decoded string
		decoded, err = url.PathUnescape(strings.TrimSpace(string(input)))
		if err != nil {
			return nil, ErrTextInvalidPercentEncoding
		}
		output = []byte(decoded)
	case "count":
		var lines, words, characters int
		inWord := false
		for _, scalar := range string(input) {
			characters++
			if scalar == '\n' {
				lines++
			}
			if unicode.IsSpace(scalar) {
				inWord = false
			} else if !inWord {
				words++
				inWord = true
			}
		}
		output = fmt.Appendf(nil, "lines: %d\nwords: %d\nchars: %d\n", lines, words, characters)
	case "sha256":
		digest := sha256.Sum256(input)
		output = fmt.Appendf(nil, "%x\n", digest)
	case "lorem":
		output = []byte(textLorem)
	case "gitignore":
		output = []byte(textGitignore)
	case "password":
		value, randomErr := transformer.randomBytes(24)
		if randomErr != nil {
			return nil, randomErr
		}
		output = []byte(base64.StdEncoding.EncodeToString(value) + "\n")
	default:
		return nil, ErrTextUnsupportedOperation
	}
	if err != nil {
		return nil, err
	}
	if len(output) > TextMaximumBytes {
		return nil, ErrTextOutputTooLarge
	}
	if !utf8.Valid(output) {
		return nil, ErrTextInvalidUTF8
	}
	return output, nil
}

func validateTextInput(input []byte) error {
	if len(input) > TextMaximumBytes {
		return ErrTextInputTooLarge
	}
	if !utf8.Valid(input) {
		return ErrTextInvalidUTF8
	}
	return nil
}

func validTextEpoch(seconds int64) bool {
	return seconds >= -62_135_596_800 && seconds <= 253_402_300_799
}

func (transformer TextTransformer) randomBytes(count int) ([]byte, error) {
	reader := transformer.Random
	if reader == nil {
		reader = rand.Reader
	}
	value := make([]byte, count)
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, ErrTextRandomGeneration
	}
	return value, nil
}

func decodeTextBase64(input []byte) ([]byte, error) {
	encoded := make([]byte, 0, len(input))
	for _, value := range input {
		if textASCIIWhitespace(value) {
			continue
		}
		valid := (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') ||
			(value >= '0' && value <= '9') || value == '+' || value == '/' || value == '='
		if !valid {
			return nil, ErrTextInvalidBase64
		}
		encoded = append(encoded, value)
	}
	if len(encoded)%4 != 0 {
		return nil, ErrTextInvalidBase64
	}
	output, err := base64.StdEncoding.Strict().DecodeString(string(encoded))
	if err != nil {
		return nil, ErrTextInvalidBase64
	}
	return output, nil
}

func encodeTextURL(input string) ([]byte, error) {
	const hex = "0123456789ABCDEF"
	var output boundedTextOutput
	for index := 0; index < len(input); index++ {
		value := input[index]
		if (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') ||
			(value >= '0' && value <= '9') || strings.IndexByte("-._~/", value) >= 0 {
			if err := output.append(value); err != nil {
				return nil, err
			}
		} else if err := output.append('%', hex[value>>4], hex[value&15]); err != nil {
			return nil, err
		}
	}
	return output.bytes, nil
}

func textASCIIWhitespace(value byte) bool {
	return value == ' ' || (value >= 9 && value <= 13)
}

// prettyTextJSON retains member order, number lexemes, and string escapes.
// Grammar validation precedes the bounded writer, so errors return no partial data.
func prettyTextJSON(input []byte) ([]byte, error) {
	depth := 0
	quoted, escaped := false, false
	for _, value := range input {
		if quoted {
			if escaped {
				escaped = false
			} else if value == '\\' {
				escaped = true
			} else if value == '"' {
				quoted = false
			}
		} else if value == '"' {
			quoted = true
		} else if value == '{' || value == '[' {
			depth++
			if depth > textMaximumJSONDepth {
				return nil, ErrTextJSONNestingTooDeep
			}
		} else if value == '}' || value == ']' {
			depth--
		}
	}
	if !json.Valid(input) {
		return nil, ErrTextInvalidJSON
	}
	var output boundedTextOutput
	depth = 0
	var previous byte
	for index := 0; index < len(input); index++ {
		value := input[index]
		if textASCIIWhitespace(value) {
			continue
		}
		var err error
		switch value {
		case '{', '[':
			err = output.append(value)
			depth++
			next := index + 1
			for next < len(input) && textASCIIWhitespace(input[next]) {
				next++
			}
			closing := byte('}')
			if value == '[' {
				closing = ']'
			}
			if err == nil && input[next] != closing {
				err = output.newline(depth)
			}
		case '}', ']':
			depth--
			opening := byte('{')
			if value == ']' {
				opening = '['
			}
			if previous != opening {
				err = output.newline(depth)
			}
			if err == nil {
				err = output.append(value)
			}
		case ',':
			err = output.append(value)
			if err == nil {
				err = output.newline(depth)
			}
		case ':':
			err = output.append(':', ' ')
		case '"':
			start := index
			index++
			for input[index] != '"' {
				if input[index] == '\\' {
					index++
				}
				index++
			}
			err = output.append(input[start : index+1]...)
		default:
			start := index
			for index+1 < len(input) && !textASCIIWhitespace(input[index+1]) &&
				input[index+1] != ',' && input[index+1] != ']' && input[index+1] != '}' {
				index++
			}
			err = output.append(input[start : index+1]...)
		}
		if err != nil {
			return nil, err
		}
		previous = value
	}
	if err := output.append('\n'); err != nil {
		return nil, err
	}
	return output.bytes, nil
}

type boundedTextOutput struct {
	bytes []byte
}

func (output *boundedTextOutput) append(addition ...byte) error {
	if len(addition) > TextMaximumBytes-len(output.bytes) {
		return ErrTextOutputTooLarge
	}
	output.bytes = append(output.bytes, addition...)
	return nil
}

func (output *boundedTextOutput) newline(depth int) error {
	if err := output.append('\n'); err != nil {
		return err
	}
	for index := 0; index < depth; index++ {
		if err := output.append(' ', ' ', ' ', ' '); err != nil {
			return err
		}
	}
	return nil
}

const textLorem = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.\n\nDuis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.\n"

const textGitignore = ".DS_Store\n*.swp\n.env\n.env.*\n!.env.example\nnode_modules/\ndist/\nbuild/\ncoverage/\n__pycache__/\n*.pyc\n.venv/\nvenv/\n.pytest_cache/\n.mypy_cache/\n*.log\n.idea/\n.vscode/\n"
