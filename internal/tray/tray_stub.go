//go:build !tray

package tray

import "fmt"

func Run() int {
	fmt.Println("clawmeter: tray mode not available in this build")
	fmt.Println("rebuild with: go build -tags tray ./cmd/clawmeter")
	return 1
}
