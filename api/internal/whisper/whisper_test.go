package whisper

import "testing"

func TestParseCppJSON(t *testing.T) {
	raw := []byte(`{"result":{"language":"en"},"transcription":[
		{"offsets":{"from":0,"to":400},"text":" Hello"},
		{"offsets":{"from":400,"to":450},"text":","},
		{"offsets":{"from":500,"to":900},"text":" world"},
		{"offsets":{"from":900,"to":1000},"text":" [MUSIC]"},
		{"offsets":{"from":1000,"to":1200},"text":" don"},
		{"offsets":{"from":1200,"to":1400},"text":"'t"}
	]}`)
	lang, words, err := ParseCppJSON(raw, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if lang != "en" || len(words) != 3 {
		t.Fatalf("lang=%s words=%+v", lang, words)
	}
	if words[0].Text != "Hello," || words[0].End != 10450 {
		t.Fatalf("punctuation not merged: %+v", words[0])
	}
	if words[2].Text != "don't" || words[2].Start != 11000 {
		t.Fatalf("subword not merged: %+v", words[2])
	}
}

func TestParseVerbose(t *testing.T) {
	lang, words, err := parseVerbose([]byte(`{"language":"english","words":[{"word":" Hi","start":0.5,"end":0.9}]}`))
	if err != nil || lang != "english" || len(words) != 1 || words[0].Start != 500 {
		t.Fatalf("%v %v %+v", err, lang, words)
	}
}
