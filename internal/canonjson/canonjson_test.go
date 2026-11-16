package canonjson

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"b": 1, "a": [true, false, null], "c": {}}`, `{"a":[true,false,null],"b":1,"c":{}}`},
		{`  [ ]  `, `[]`},
		{`-0`, `0`},
		{`9007199254740991`, `9007199254740991`},
		{`-9007199254740991`, `-9007199254740991`},
		// rfc 8785 section 3.2.2.2: the short escapes, lowercase \u00xx for
		// other controls, and everything else as utf-8 (the solidus too)
		{`"\u0008\t\n\f\r\"\\\u001F\u007f\/€"`, "\"\\b\\t\\n\\f\\r\\\"\\\\\\u001f\u007f/€\""},
		// rfc 8785 section 3.2.3: sorting by utf-16 code units
		{`{"\u20ac":"Euro Sign","\r":"Carriage Return","\ufb33":"Hebrew Letter Dalet With Dagesh",` +
			`"1":"One","\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control","\u00f6":"Latin Small Letter O With Diaeresis"}`,
			"{\"\\r\":\"Carriage Return\",\"1\":\"One\",\"\u0080\":\"Control\",\"ö\":\"Latin Small Letter O With Diaeresis\"," +
				"\"€\":\"Euro Sign\",\"\U0001F600\":\"Emoji: Grinning Face\",\"\ufb33\":\"Hebrew Letter Dalet With Dagesh\"}"},
	} {
		got, err := Canonicalize([]byte(c.in))
		require.NoError(t, err, c.in)
		require.Equal(t, c.want, string(got), c.in)
		// the canonical form is a fixed point
		again, err := Canonicalize(got)
		require.NoError(t, err)
		require.Equal(t, got, again)
	}
}

func TestCanonicalizeRefuses(t *testing.T) {
	for _, in := range []string{
		`1.5`, `1e3`, `9007199254740992`, `-9007199254740992`,
		`{"a":1,"a":2}`, `{"a":1} {}`, `{"a":`, "\"\xff\"",
	} {
		_, err := Canonicalize([]byte(in))
		require.Error(t, err, in)
	}
}

func TestMarshal(t *testing.T) {
	type inner struct {
		Z string `json:"z"`
		A *int   `json:"a"`
	}
	got, err := Marshal(map[string]any{"k": inner{Z: "ç"}, "b": []int{3, 1}})
	require.NoError(t, err)
	require.Equal(t, `{"b":[3,1],"k":{"a":null,"z":"ç"}}`, string(got))
	_, err = Marshal(map[string]any{"f": 0.5})
	require.Error(t, err)
}
