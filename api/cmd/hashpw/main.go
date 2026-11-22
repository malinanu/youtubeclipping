// hashpw prints a bcrypt hash for ADMIN_PASSWORD_HASH.
//
//	docker compose run --rm api hashpw 'my-password'
package main

import (
	"bufio"
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
	fmt.Println(string(h))
}
