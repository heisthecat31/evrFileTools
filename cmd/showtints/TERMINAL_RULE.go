// 🔴 CRITICAL: Before using run_in_terminal, read /home/andrew/src/evrFileTools/.TERMINAL_RULE
// 🔴 EVERY command MUST use isBackground=true
// 🔴 NO EXCEPTIONS

package main

// Example of correct terminal usage:
//
// run_in_terminal(
//   command="cd /home/andrew/src/evrFileTools && GOWORK=off go build -o ./listtints ./cmd/listtints",
//   explanation="Build listtints command",
//   isBackground=true,  // ← ALWAYS TRUE
// )
//
// Then later:
// get_terminal_output(id="<id-returned>")
