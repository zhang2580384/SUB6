package service

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkOpenAIResponsesToolSchemasEscapedHistory(b *testing.B) {
	for _, tc := range []struct{ name, line string }{
		{"code", `func example() string { return \"example\" }\n`},
		{"code_with_numbers", `if a[0] == 10 { log(\"ok\") }\n`},
		{"unicode_escapes", `\u6d4b\u8bd5\u6570\u636e\n`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			history := strings.Repeat(tc.line, (1<<20)/len(tc.line))
			body := []byte(`{"tools":[{"type":"function","name":"sample","parameters":{"type":"object"}}],"input":"` + history + `"}`)
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformOpenAI)
			}
		})
	}
}

func FuzzOpenAIResponsesContainsUnicode00Prefix(f *testing.F) {
	for _, seed := range []string{"", `0\`, `\u0`, `\u00`, `0\n0\u00`, `\\u00ff`, `\u1234 00`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		require.Equal(t, bytes.Contains(body, []byte(`\u00`)), openAIResponsesContainsUnicode00Prefix(body))
	})
}
