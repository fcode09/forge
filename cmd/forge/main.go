// Command forge builds, packages and ships Docker images and triggers deltactl on the server.
package main

import (
	"fmt"

	"github.com/fcode09/forge/internal/version"
)

func main() {
	fmt.Println("forge", version.Version)
}
