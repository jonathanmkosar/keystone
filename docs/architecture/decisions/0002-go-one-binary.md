# 0002. Keystone is written in Go and built to one binary

November 24, 2026 · decided in P0-03

## The question

What language is the server written in, and what does an install consist of? The earlier Keystone was Python, with an environment to set up on each machine. Waymarks is Go.

## The decision

Go. An install is one binary, `keystone`, and one data folder. The binary is the server and the command line. The settings registry and the built web interface are compiled into it.

The project scripts (`rebuild`, `verify`, `doctor`, `snapshot`) are Go too, run with `go run`, so the same commands work on Windows, macOS and Linux with no shell differences.

The module uses the standard library unless a dependency earns its place. Each outside module is named in a decision record.

## Why

- One file to copy and run. Windows is the first target, and nothing has to be installed beside the binary.
- It matches Waymarks, so the two projects share habits and client code.
- The standard library covers the HTTP server, streaming, embedding files and testing.

## What it costs

- The earlier Python Keystone and New Keystone are sources to borrow ideas from, not code to reuse. What is borrowed is rewritten.
- Go 1.24 or later is needed to build. It was checked on Windows with Go 1.26.
