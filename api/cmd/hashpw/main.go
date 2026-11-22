// hashpw prints a value for ADMIN_PASSWORD_HASH: the bcrypt hash, base64-encoded
// so it contains no "$" and can be pasted into .env files or Portainer as-is.
//
//	docker run --rm ghcr.io/malinanu/clipforge-backend hashpw 'my-password'
package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	var pw string
	if len(os.Args) > 1 {
		pw = os.Args[1]
	} else {
		fmt.Fprint(os.Stderr, "Password: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	if len(pw) < 10 {
		fmt.Fprintln(os.Stderr, "password must be at least 10 characters")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(h))
}
