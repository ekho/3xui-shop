package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

func runHealthcheck(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || (host != "" && host != "0.0.0.0" && host != "127.0.0.1" && host != "::" && host != "::1") {
		return errors.New("invalid healthcheck listen address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("invalid healthcheck port")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loopback := "127.0.0.1"
	if host == "::" || host == "::1" {
		loopback = "::1"
	}
	resp, err := client.Get("http://" + net.JoinHostPort(loopback, port) + "/readyz")
	if err != nil {
		return errors.New("backend not ready")
	}
	defer resp.Body.Close()
	var result struct {
		OK bool `json:"ok"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 128)).Decode(&result) != nil || !result.OK {
		return errors.New("backend not ready")
	}
	return nil
}
