# Implement the CLI in Go

Plugman will be implemented in Go as one executable for Linux, macOS, and Windows. Go provides direct cross-compilation, strong standard-library support for HTTP, JSON, subprocesses, concurrency, and filesystem work, and does not require users to install Node.js or Python; this is a better fit for the standalone product than TypeScript and a lower-complexity implementation path than Rust for the same user-visible result.
