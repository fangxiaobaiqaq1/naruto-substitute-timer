//go:build !windows || !amd64

package leidian

import (
	"context"
	"errors"
	"image"
)

type Options struct {
	InstallDir  string
	ConsolePath string
	ADBPath     string
	Index       int
	Serial      string
	Package     string
	Connect     bool
}

type Client struct{}

type Instance struct {
	Root           string
	Index          int
	Name           string
	Running        bool
	ProcessStarted bool
	AndroidStarted bool
	PID            int
	Serial         string
	Resolution     string
}

type ProbeResult struct{}

func Open(Options) (*Client, error) { return nil, errors.New("雷电适配仅支持 Windows amd64") }
func OpenAuto(Options) (*Client, error) {
	return nil, errors.New("雷电适配仅支持 Windows amd64")
}
func DiscoverInstallDirs() []string { return nil }
func DiscoverInstallDir() string    { return "" }
func ListInstances(context.Context, string) ([]Instance, error) {
	return nil, errors.New("雷电适配仅支持 Windows amd64")
}
func ProbeContext(context.Context, Options) ProbeResult { return ProbeResult{} }
func Probe(Options) ProbeResult                         { return ProbeResult{} }
func (c *Client) Capture() (*image.RGBA, error) {
	return nil, errors.New("雷电适配仅支持 Windows amd64")
}
func (c *Client) Close() error   { return nil }
func (c *Client) Source() string { return "" }
