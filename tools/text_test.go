package tools

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestTextTransformsNativeSamples(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"plain", "Саха\n\ttext ", "Саха\n\ttext "},
		{"json", ` {"z":9007199254740993,"a":[true,{},"\\u1234",1e+09]} `,
			"{\n    \"z\": 9007199254740993,\n    \"a\": [\n        true,\n        {},\n        \"\\\\u1234\",\n        1e+09\n    ]\n}\n"},
		{"json", " \"literal [ ] { }\" ", "\"literal [ ] { }\"\n"},
		{"json", "true", "true\n"},
		{"base64-encode", "Саха", "0KHQsNGF0LA=\n"},
		{"base64-encode", "", "\n"},
		{"base64-decode", " \t0K\nHQs\vNGF\f0LA=\r", "Саха"},
		{"base64-decode", "", ""},
		{"url-encode", "\u2002 /café +?&=#\n", "/caf%C3%A9%20%2B%3F%26%3D%23"},
		{"url-decode", " \t/caf%C3%A9+%20\n", "/café+ "},
		{"count", "A e\u0301\n猫\u00a0b 👩‍💻", "lines: 1\nwords: 5\nchars: 12\n"},
		{"count", "", "lines: 0\nwords: 0\nchars: 0\n"},
		{"count", "a\r\nb", "lines: 1\nwords: 2\nchars: 4\n"},
		{"sha256", "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n"},
		{"gitignore", "", ".DS_Store\n*.swp\n.env\n.env.*\n!.env.example\nnode_modules/\ndist/\nbuild/\ncoverage/\n__pycache__/\n*.pyc\n.venv/\nvenv/\n.pytest_cache/\n.mypy_cache/\n*.log\n.idea/\n.vscode/\n"},
		{"lorem", "", "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.\n\nDuis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := (TextTransformer{}).Transform(test.name, []byte(test.input))
			if err != nil || string(got) != test.want {
				t.Fatalf("Transform = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestTextEpochBoundariesAndClock(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"0", "1970-01-01T00:00:00Z\n"},
		{" \n-1\t", "1969-12-31T23:59:59Z\n"},
		{"+1", "1970-01-01T00:00:01Z\n"},
		{"-62135596800", "0001-01-01T00:00:00Z\n"},
		{"253402300799", "9999-12-31T23:59:59Z\n"},
	} {
		got, err := (TextTransformer{}).Transform("epoch-to-date", []byte(test.input))
		if err != nil || string(got) != test.want {
			t.Fatalf("epoch-to-date(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	transformer := TextTransformer{Now: func() time.Time { return time.Unix(-1, 999999999) }}
	if got, err := transformer.Transform("epoch", nil); err != nil || string(got) != "-1\n" {
		t.Fatalf("epoch must round down: got %q, %v", got, err)
	}
	transformer.Now = func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }
	if got, err := transformer.Transform("epoch", nil); !errors.Is(err, ErrTextInvalidEpoch) || got != nil {
		t.Fatal("out-of-range clock must return no output and invalid epoch")
	}
}

func TestTextRejectsMalformedInputWithoutPartialResult(t *testing.T) {
	for _, test := range []struct {
		name, input string
		want        error
	}{
		{"json", "", ErrTextInvalidJSON},
		{"json", `{"a":1,}`, ErrTextInvalidJSON},
		{"json", `[1,]`, ErrTextInvalidJSON},
		{"json", `{} {}`, ErrTextInvalidJSON},
		{"json", "\ufeff{}", ErrTextInvalidJSON},
		{"json", "[}", ErrTextInvalidJSON},
		{"json", `"unterminated`, ErrTextInvalidJSON},
		{"base64-decode", "Zm9v$", ErrTextInvalidBase64},
		{"base64-decode", "Zg", ErrTextInvalidBase64},
		{"base64-decode", "Zh==", ErrTextInvalidBase64},
		{"base64-decode", "Zm9=", ErrTextInvalidBase64},
		{"base64-decode", "Zg===", ErrTextInvalidBase64},
		{"base64-decode", "Zg==Yg==", ErrTextInvalidBase64},
		{"base64-decode", "Zg==\u00a0", ErrTextInvalidBase64},
		{"base64-decode", "/w==", ErrTextInvalidUTF8},
		{"url-decode", "valid%20prefix%", ErrTextInvalidPercentEncoding},
		{"url-decode", "%2G", ErrTextInvalidPercentEncoding},
		{"url-decode", "%FF", ErrTextInvalidUTF8},
		{"epoch-to-date", "", ErrTextInvalidEpoch},
		{"epoch-to-date", "1.5", ErrTextInvalidEpoch},
		{"epoch-to-date", "1e3", ErrTextInvalidEpoch},
		{"epoch-to-date", "-62135596801", ErrTextInvalidEpoch},
		{"epoch-to-date", "253402300800", ErrTextInvalidEpoch},
		{"epoch-to-date", "999999999999999999999999", ErrTextInvalidEpoch},
		{"unsupported", "", ErrTextUnsupportedOperation},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := (TextTransformer{}).Transform(test.name, []byte(test.input))
			if !errors.Is(err, test.want) || got != nil {
				t.Fatalf("expected %v and no output; got error %v, output length %d", test.want, err, len(got))
			}
		})
	}
	for _, operation := range []string{"plain", "json", "base64-encode", "base64-decode", "url-encode", "url-decode", "count", "sha256"} {
		got, err := (TextTransformer{}).Transform(operation, []byte{0xff})
		if !errors.Is(err, ErrTextInvalidUTF8) || got != nil {
			t.Fatalf("%s must reject non-UTF-8 input with no output", operation)
		}
	}
}

func TestTextInputAndOutputBounds(t *testing.T) {
	maximum := bytes.Repeat([]byte{'a'}, TextMaximumBytes)
	got, err := (TextTransformer{}).Transform("plain", maximum)
	if err != nil || !bytes.Equal(got, maximum) {
		t.Fatal("exactly 1 MiB plain text should pass unchanged")
	}
	for _, operation := range []string{"plain", "uuid", "epoch", "json", "password"} {
		got, err := (TextTransformer{}).Transform(operation, bytes.Repeat([]byte{'a'}, TextMaximumBytes+1))
		if !errors.Is(err, ErrTextInputTooLarge) || got != nil {
			t.Fatalf("%s must reject input larger than 1 MiB", operation)
		}
	}
	for _, test := range []struct{ operation, input string }{
		{"base64-encode", strings.Repeat("a", 3*(TextMaximumBytes/4))},
		{"url-encode", strings.Repeat("?", TextMaximumBytes/3+1)},
		{"json", "[" + strings.Repeat("0,", 200_000) + "0]"},
	} {
		got, err := (TextTransformer{}).Transform(test.operation, []byte(test.input))
		if !errors.Is(err, ErrTextOutputTooLarge) || got != nil {
			t.Fatalf("%s must reject expanded output larger than 1 MiB without a partial result", test.operation)
		}
	}
	if got, err := (TextTransformer{}).Transform("url-encode", maximum); err != nil || len(got) != TextMaximumBytes {
		t.Fatal("exactly 1 MiB URL-safe output should pass")
	}
	if got, err := (TextTransformer{}).Transform("base64-encode", maximum[:3*(TextMaximumBytes/4)-3]); err != nil || len(got) > TextMaximumBytes {
		t.Fatal("Base64 output just within the limit should pass")
	}
}

func TestTextJSONNestingBound(t *testing.T) {
	atLimit := strings.Repeat("[", 128) + "0" + strings.Repeat("]", 128)
	if got, err := (TextTransformer{}).Transform("json", []byte(atLimit)); err != nil || len(got) == 0 {
		t.Fatalf("128 nesting levels should pass: %v", err)
	}
	got, err := (TextTransformer{}).Transform("json", []byte("["+atLimit+"]"))
	if !errors.Is(err, ErrTextJSONNestingTooDeep) || got != nil {
		t.Fatal("129 nesting levels must fail without a partial result")
	}
	quoted := `"` + strings.Repeat("[", 129) + `"`
	if _, err := (TextTransformer{}).Transform("json", []byte(quoted)); err != nil {
		t.Fatalf("brackets in strings do not add depth: %v", err)
	}
}

func TestTextRandomUUIDAndPassword(t *testing.T) {
	transformer := TextTransformer{Random: bytes.NewReader(make([]byte, 16))}
	uuid, err := transformer.Transform("uuid", nil)
	if err != nil || string(uuid) != "00000000-0000-4000-8000-000000000000\n" {
		t.Fatal("UUID must set RFC 9562 version 4 and variant bits")
	}
	transformer.Random = bytes.NewReader(bytes.Repeat([]byte{255}, 16))
	uuid, err = transformer.Transform("uuid", nil)
	if err != nil || string(uuid) != "FFFFFFFF-FFFF-4FFF-BFFF-FFFFFFFFFFFF\n" {
		t.Fatal("UUID must retain random bits while setting version and variant")
	}
	transformer.Random = bytes.NewReader(make([]byte, 24))
	password, err := transformer.Transform("password", nil)
	if err != nil || len(password) != 33 || password[32] != '\n' {
		t.Fatal("password must contain 24 random bytes as 32 Base64 characters plus LF")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(string(password), "\n"))
	if err != nil || !bytes.Equal(decoded, make([]byte, 24)) {
		t.Fatal("password must use exactly the injected random bytes")
	}
	for _, operation := range []string{"uuid", "password"} {
		for _, reader := range []io.Reader{bytes.NewReader(nil), textErrorReader{}, bytes.NewReader(make([]byte, 15))} {
			got, err := (TextTransformer{Random: reader}).Transform(operation, nil)
			if !errors.Is(err, ErrTextRandomGeneration) || got != nil {
				t.Fatalf("%s random-source failure must produce no output", operation)
			}
		}
	}
}

type textErrorReader struct{}

func (textErrorReader) Read([]byte) (int, error) { return 0, errors.New("unavailable") }

func TestReadTextInputIsBoundedAndAtomic(t *testing.T) {
	reader := bytes.NewBuffer(bytes.Repeat([]byte{'x'}, TextMaximumBytes+100))
	got, err := ReadTextInput(reader)
	if !errors.Is(err, ErrTextInputTooLarge) || got != nil || reader.Len() != 99 {
		t.Fatal("bounded reader must stop after the first byte beyond the limit")
	}
	if got, err := ReadTextInput(bytes.NewReader([]byte{0xff})); !errors.Is(err, ErrTextInvalidUTF8) || got != nil {
		t.Fatal("non-UTF-8 input must fail with no returned content")
	}
	if got, err := ReadTextInput(textErrorReader{}); err == nil || got != nil {
		t.Fatal("read errors must produce no returned content")
	}
	maximum := bytes.Repeat([]byte{'x'}, TextMaximumBytes)
	if got, err := ReadTextInput(bytes.NewReader(maximum)); err != nil || !bytes.Equal(got, maximum) {
		t.Fatal("exactly 1 MiB must be accepted")
	}
}
