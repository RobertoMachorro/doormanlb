package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Client struct {
	httpClient *http.Client
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func NewClient() *Client {
	return &Client{httpClient: &http.Client{}}
}

func (c *Client) Forward(ctx context.Context, upstreamBaseURL string, request *http.Request, writer http.ResponseWriter) error {
	response, err := c.Fetch(ctx, upstreamBaseURL, request)
	if err != nil {
		return err
	}

	response.WriteTo(writer)
	return nil
}

func (c *Client) Fetch(ctx context.Context, upstreamBaseURL string, request *http.Request) (*Response, error) {
	targetURL, err := buildTargetURL(upstreamBaseURL, request.URL)
	if err != nil {
		return nil, err
	}

	proxyRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building upstream request: %w", err)
	}

	cloneHeaders(request.Header, proxyRequest.Header)

	response, err := c.httpClient.Do(proxyRequest)
	if err != nil {
		return nil, fmt.Errorf("performing upstream request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading upstream response: %w", err)
	}

	return &Response{
		StatusCode: response.StatusCode,
		Header:     cloneHeader(response.Header),
		Body:       body,
	}, nil
}

func (r *Response) WriteTo(writer http.ResponseWriter) {
	cloneHeaders(r.Header, writer.Header())
	writer.WriteHeader(r.StatusCode)
	_, _ = writer.Write(r.Body)
}

func buildTargetURL(upstreamBaseURL string, requestURL *url.URL) (string, error) {
	base, err := url.Parse(upstreamBaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid upstream service url %q: %w", upstreamBaseURL, err)
	}

	resolved := base.ResolveReference(&url.URL{Path: requestURL.Path, RawQuery: requestURL.RawQuery})
	return resolved.String(), nil
}

func cloneHeaders(source, destination http.Header) {
	for key, values := range source {
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

func cloneHeader(source http.Header) http.Header {
	destination := make(http.Header, len(source))
	cloneHeaders(source, destination)
	return destination
}
