package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alikatgh/le-cli/tools"
	"github.com/spf13/cobra"
)

func executeTextForTest(input io.Reader, transformer tools.TextTransformer, arguments ...string) ([]byte, error) {
	root := &cobra.Command{Use: "le", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(textCmdWithTransformer(transformer))
	var output bytes.Buffer
	root.SetIn(input)
	root.SetOut(&output)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"text"}, arguments...))
	err := root.Execute()
	return output.Bytes(), err
}

func TestTextCommandsUseRootInputAndOutput(t *testing.T) {
	for _, test := range []struct {
		arguments   []string
		input, want string
	}{
		{[]string{"plain"}, "Саха\n", "Саха\n"},
		{[]string{"json"}, `{"b":2,"a":1}`, "{\n    \"b\": 2,\n    \"a\": 1\n}\n"},
		{[]string{"base64-encode"}, "hello", "aGVsbG8=\n"},
		{[]string{"base64-decode"}, "aGVs bG8=\n", "hello"},
		{[]string{"url-encode"}, " /x+y?z=1 \n", "/x%2By%3Fz%3D1"},
		{[]string{"url-decode"}, "%2Fx+y%20", "/x+y "},
		{[]string{"count"}, "one\ntwo", "lines: 1\nwords: 2\nchars: 7\n"},
		{[]string{"sha256"}, "abc", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n"},
		{[]string{"epoch-to-date"}, "-1", "1969-12-31T23:59:59Z\n"},
		{[]string{"epoch-to-date"}, "0", "1970-01-01T00:00:00Z\n"},
		{[]string{"epoch-to-date", "--seconds=-1"}, "ignored", "1969-12-31T23:59:59Z\n"},
		{[]string{"epoch-to-date", "--seconds", "0"}, "ignored", "1970-01-01T00:00:00Z\n"},
	} {
		t.Run(strings.Join(test.arguments, " "), func(t *testing.T) {
			got, err := executeTextForTest(bytes.NewBufferString(test.input), tools.TextTransformer{}, test.arguments...)
			if err != nil || string(got) != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestTextCommandsDoNotWritePartialInvalidResults(t *testing.T) {
	for _, test := range []struct {
		operation string
		input     []byte
		want      error
	}{
		{"json", []byte(`{"a":1,}`), tools.ErrTextInvalidJSON},
		{"json", []byte(strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129)), tools.ErrTextJSONNestingTooDeep},
		{"base64-decode", []byte("Zm9v$"), tools.ErrTextInvalidBase64},
		{"base64-decode", []byte("Zh=="), tools.ErrTextInvalidBase64},
		{"base64-decode", []byte("/w=="), tools.ErrTextInvalidUTF8},
		{"url-decode", []byte("first%20then%"), tools.ErrTextInvalidPercentEncoding},
		{"url-decode", []byte("%FF"), tools.ErrTextInvalidUTF8},
		{"plain", []byte{0xff}, tools.ErrTextInvalidUTF8},
		{"epoch-to-date", []byte("1.2"), tools.ErrTextInvalidEpoch},
		{"plain", bytes.Repeat([]byte{'a'}, tools.TextMaximumBytes+1), tools.ErrTextInputTooLarge},
		{"base64-encode", bytes.Repeat([]byte{'a'}, 3*(tools.TextMaximumBytes/4)), tools.ErrTextOutputTooLarge},
		{"url-encode", bytes.Repeat([]byte{'?'}, tools.TextMaximumBytes/3+1), tools.ErrTextOutputTooLarge},
		{"json", []byte("[" + strings.Repeat("0,", 200_000) + "0]"), tools.ErrTextOutputTooLarge},
	} {
		t.Run(test.operation, func(t *testing.T) {
			got, err := executeTextForTest(bytes.NewReader(test.input), tools.TextTransformer{}, test.operation)
			if !errors.Is(err, test.want) || len(got) != 0 {
				t.Fatalf("expected %v and empty stdout; got %v and %d bytes", test.want, err, len(got))
			}
		})
	}
}

func TestTextGeneratorsDoNotConsumeStdin(t *testing.T) {
	for _, operation := range []string{"uuid", "epoch", "lorem", "gitignore", "password"} {
		transformer := tools.TextTransformer{
			Now:    func() time.Time { return time.Unix(0, 999999999) },
			Random: bytes.NewReader(make([]byte, 24)),
		}
		got, err := executeTextForTest(textUnreadableInput{}, transformer, operation)
		if err != nil || len(got) == 0 {
			t.Fatalf("%s should generate output without consuming stdin", operation)
		}
		if operation == "epoch" && string(got) != "0\n" {
			t.Fatal("epoch command must use the injected clock")
		}
	}
	if got, err := executeTextForTest(textUnreadableInput{}, tools.TextTransformer{}, "epoch-to-date", "--seconds=-1"); err != nil || len(got) == 0 {
		t.Fatal("--seconds must avoid reading stdin")
	}
}

type textUnreadableInput struct{}

func (textUnreadableInput) Read([]byte) (int, error) { return 0, errors.New("stdin must not be read") }

func TestTextRandomAndReadErrorsHaveEmptyStdout(t *testing.T) {
	for _, operation := range []string{"uuid", "password"} {
		got, err := executeTextForTest(bytes.NewBuffer(nil), tools.TextTransformer{Random: textUnreadableInput{}}, operation)
		if !errors.Is(err, tools.ErrTextRandomGeneration) || len(got) != 0 {
			t.Fatalf("%s random-source failure should leave stdout empty", operation)
		}
	}
	if got, err := executeTextForTest(textUnreadableInput{}, tools.TextTransformer{}, "plain"); err == nil || len(got) != 0 {
		t.Fatal("stdin failure should leave stdout empty")
	}
}

func TestTextHasAllFourteenNativeTools(t *testing.T) {
	command := textCmd()
	want := []string{"uuid", "json", "epoch", "epoch-to-date", "plain", "base64-encode", "base64-decode", "url-encode", "url-decode", "count", "sha256", "lorem", "gitignore", "password"}
	if len(command.Commands()) != len(want) {
		t.Fatalf("got %d tools, want 14", len(command.Commands()))
	}
	for _, name := range want {
		found, _, err := command.Find([]string{name})
		if err != nil || found == command || found.Name() != name {
			t.Fatalf("missing native text tool %s", name)
		}
	}
}

func TestTextRejectsUnexpectedArguments(t *testing.T) {
	got, err := executeTextForTest(bytes.NewBufferString("ignored"), tools.TextTransformer{}, "plain", "extra")
	if !errors.Is(err, errUsage) || len(got) != 0 {
		t.Fatal("extra positional arguments should be usage errors without stdout")
	}
}

func TestRootRegistersTextAndPreservesCheckUsageExit(t *testing.T) {
	root := newRoot("test", false)
	root.SetIn(bytes.NewBufferString("abc"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"text", "sha256"})
	if err := root.Execute(); err != nil || !bytes.HasPrefix(out.Bytes(), []byte("ba7816bf")) {
		t.Fatalf("root text command unavailable: %v", err)
	}
	for _, port := range []string{"notaport", "+1", " 80", "65536"} {
		root = newRoot("test", false)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{"check", port})
		if err := root.Execute(); exitCodeFor(err) != 2 {
			t.Errorf("check %q exit=%d, want usage exit2", port, exitCodeFor(err))
		}
	}
}
