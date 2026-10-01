//go:build !(windows && amd64)

package mumu

import (
	"context"
	"errors"
	"image"
	"time"
)

var errUnsupported = errors.New("MuMu 截图 SDK 仅支持 Windows x64")

// Client is a placeholder on non-Windows hosts; every operation fails. It keeps
// the tooling and tests that only need the data types compiling elsewhere.
type Client struct{}

func Open(Options) (*Client, error)               { return nil, errUnsupported }
func OpenAuto(Options) (*Client, error)           { return nil, errUnsupported }
func (c *Client) Capture() (*image.RGBA, error)   { return nil, errUnsupported }
func (c *Client) Close() error                    { return nil }
func (c *Client) DLLPath() string                 { return "" }
func (c *Client) Source() string                  { return "" }
func FindDLL(string) (string, error)              { return "", errUnsupported }
func FindDLLCandidates(string) ([]SDKFile, error) { return nil, errUnsupported }
func DiscoverInstallation() (string, error)       { return "", errUnsupported }
func DiscoverInstallations() ([]string, error)    { return nil, errUnsupported }
func ListInstances(context.Context, string) ([]Instance, error) {
	return nil, errUnsupported
}
func ListProcesses(context.Context) ([]ProcessEvidence, error) { return nil, errUnsupported }
func ListProcessChoices(context.Context, string) ([]ProcessChoice, error) {
	return nil, errUnsupported
}

func DiscoverInventory(context.Context, ...string) Inventory {
	return Inventory{GeneratedAt: time.Now(), Errors: []string{errUnsupported.Error()}}
}

func Probe(options Options) ProbeResult {
	now := time.Now()
	return ProbeResult{StartedAt: now, EndedAt: now, Requested: options, FailureStage: ProbeStageLoadSDK, Error: errUnsupported.Error()}
}
