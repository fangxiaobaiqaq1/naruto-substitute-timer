package leidian

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func canonicalRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	absolute, err := filepath.Abs(root)
	if err == nil {
		root = absolute
	}
	return filepath.Clean(root)
}

type list2Row struct {
	Index   int
	Name    string
	Running bool
	// TopHWND and BindHWND are list2 fields 3 and 4 (1-based): the LDPlayer main
	// frame and the render (bind) window. Zero when absent or invalid.
	TopHWND  uintptr
	BindHWND uintptr
	PID      int
	VBoxPID  int
	Width    int
	Height   int
	DPI      int
	Raw      string
}

func decodeConsoleText(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), data)
	if err == nil {
		return string(decoded)
	}
	return string(data)
}

func parseList2(data []byte, root string) ([]Instance, error) {
	var result []Instance
	for _, line := range strings.Split(strings.ReplaceAll(decodeConsoleText(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || index < 0 {
			continue
		}
		row := list2Row{Index: index, Name: strings.TrimSpace(fields[1]), Raw: line}
		// list2 columns: index, title, top HWND, bind HWND, android started,
		// PID, VBox PID, width, height, dpi.
		if len(fields) > 2 {
			row.TopHWND = parseHWND(fields[2])
		}
		if len(fields) > 3 {
			row.BindHWND = parseHWND(fields[3])
		}
		if len(fields) > 4 {
			row.Running = strings.TrimSpace(fields[4]) == "1"
		}
		if len(fields) > 5 {
			row.PID, _ = strconv.Atoi(strings.TrimSpace(fields[5]))
		}
		if len(fields) > 6 {
			row.VBoxPID, _ = strconv.Atoi(strings.TrimSpace(fields[6]))
		}
		if len(fields) > 7 {
			row.Width, _ = strconv.Atoi(strings.TrimSpace(fields[7]))
		}
		if len(fields) > 8 {
			row.Height, _ = strconv.Atoi(strings.TrimSpace(fields[8]))
		}
		if len(fields) > 9 {
			row.DPI, _ = strconv.Atoi(strings.TrimSpace(fields[9]))
		}
		if row.Name == "" {
			row.Name = "雷电"
		}
		result = append(result, Instance{
			Root: canonicalRoot(root), Index: row.Index, Name: row.Name,
			Running:        row.Running || row.PID > 0 || row.VBoxPID > 0,
			ProcessStarted: row.Running || row.PID > 0,
			AndroidStarted: row.Running,
			PID:            row.PID, TopHWND: row.TopHWND, BindHWND: row.BindHWND, Serial: fmt.Sprintf("%s:%d", DefaultADBSerialHost, DefaultADBSerialBasePort+row.Index),
			Resolution: func() string {
				if row.Width > 0 && row.Height > 0 {
					return fmt.Sprintf("%dx%d", row.Width, row.Height)
				}
				return ""
			}(),
		})
	}
	if len(result) == 0 {
		return nil, errors.New("雷电 list2 未返回实例")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result, nil
}

// parseHWND parses a decimal list2 window handle; non-positive or malformed
// values (stopped instances report 0 or -1) yield 0.
func parseHWND(field string) uintptr {
	value, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return uintptr(value)
}
