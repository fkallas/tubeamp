package parse

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AccountInfo extracts the signed-in account name from an account/account_menu
// response. signedIn is true when the response carries an
// activeAccountHeaderRenderer (the signed-in menu); name is its accountName
// runs joined together. A logged-out menu lacks that renderer and yields
// ("", false, nil) — a valid result, not an error. Only malformed JSON returns
// a non-nil error; the function never panics on missing keys.
//
// Navigation mirrors ytmusicapi's get_account_info:
//
//	actions[0].openPopupAction.popup.multiPageMenuRenderer.header
//	  .activeAccountHeaderRenderer.accountName.runs[*].text
func AccountInfo(raw []byte) (name string, signedIn bool, err error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", false, fmt.Errorf("parse.AccountInfo: %w", err)
	}

	header := asMap(getPath(root,
		"actions", "0", "openPopupAction", "popup",
		"multiPageMenuRenderer", "header", "activeAccountHeaderRenderer"))
	if header == nil {
		// Logged-out menu: no active account header.
		return "", false, nil
	}

	name = runsText(getPath(header, "accountName", "runs"))
	return name, true, nil
}

// runsText concatenates the text of a runs slice (v is the runs []any). Missing
// or non-string fields are skipped.
func runsText(v any) string {
	var b strings.Builder
	for _, run := range getSlice(v) {
		b.WriteString(str(getPath(asMap(run), "text")))
	}
	return b.String()
}
