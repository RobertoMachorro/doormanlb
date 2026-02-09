package keybuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type Options struct {
	IgnoreParameters bool
}

func Build(request *http.Request, options Options) string {
	if request == nil || request.URL == nil {
		return ""
	}

	keyBuilder := strings.Builder{}
	keyBuilder.WriteString(request.URL.Path)

	if !options.IgnoreParameters {
		normalized := normalizeQuery(request.URL.Query())
		if normalized != "" {
			keyBuilder.WriteString("?")
			keyBuilder.WriteString(normalized)
		}
	}

	hash := sha256.Sum256([]byte(keyBuilder.String()))
	return hex.EncodeToString(hash[:])
}

func normalizeQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		vals := append([]string(nil), values[key]...)
		sort.Strings(vals)
		for _, value := range vals {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}

	return strings.Join(parts, "&")
}
