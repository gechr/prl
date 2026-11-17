package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"text/scanner"
)

// GraphQL uses POST for reads too. Inspect a replayable body without consuming
// the outgoing request. Unrecognised documents conservatively count as writes.
func readOnlyGraphQL(req *http.Request) bool {
	if req.Method != http.MethodPost || req.GetBody == nil ||
		(req.URL.Path != "/graphql" && req.URL.Path != "/api/graphql") {
		return false
	}
	body, err := req.GetBody()
	if err != nil {
		return false
	}
	var payload struct {
		Query string `json:"query"`
	}
	err = json.NewDecoder(body).Decode(&payload)
	closeErr := body.Close()
	return err == nil && closeErr == nil && readOnlyGraphQLDocument(payload.Query)
}

// Recognise query and fragment definitions at the document level, ignoring
// field names, strings and comments. A document containing any mutation is
// treated as a write regardless of which named operation was selected.
func readOnlyGraphQLDocument(query string) bool {
	var s scanner.Scanner
	s.Init(strings.NewReader(query))
	s.Mode = scanner.ScanIdents | scanner.ScanStrings
	s.Error = func(*scanner.Scanner, string) {} // never log request contents
	depth := 0
	definition, selection, foundQuery := true, false, false
	for tok := s.Scan(); tok != scanner.EOF; tok = s.Scan() {
		if tok == '#' {
			for s.Peek() != '\n' && s.Peek() != scanner.EOF {
				s.Next()
			}
			continue
		}
		if definition {
			switch {
			case tok == '{':
				foundQuery = true
			case tok == scanner.Ident && s.TokenText() == "query":
				foundQuery = true
			case tok == scanner.Ident && s.TokenText() == "fragment":
			default:
				return false
			}
			definition = false
		}
		switch tok {
		case '{', '(', '[':
			if tok == '{' && depth == 0 {
				selection = true
			}
			depth++
		case '}', ')', ']':
			depth--
			if depth < 0 {
				return false
			}
			if depth == 0 && selection {
				definition, selection = true, false
			}
		}
	}
	return foundQuery && definition && depth == 0 && s.ErrorCount == 0
}
