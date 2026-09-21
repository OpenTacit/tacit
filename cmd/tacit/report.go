// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"os"
)

// report is the console voice every wiring command speaks: one line per fact,
// each behind a fixed-width prefix so the outcomes line up down the left edge
// and a member can scan a run without reading it. connect, disconnect and
// doctor all print this way, and they used to do it three times over — two
// identical types and, in disconnect, the prefixes typed out as string
// literals at each call.
//
// The prefixes are product voice, and their widths are the point: "   ok  ",
// " todo  ", " warn  ", " FAIL  " and "    -  " all measure seven characters,
// so nothing after them shifts.
type report struct {
	// w is where the report prints; nil means stdout. Failures still go to
	// stderr, because a caller redirecting output is usually after the facts,
	// not the problems.
	w io.Writer

	fails int
	warns int
	todos int

	// todoLines is the text of every todo, kept so a caller can say again at
	// the end what it asked for in the middle. A run that wires four tools
	// prints forty lines, and the member who scrolls past one `todo` has a
	// tool that cannot load OpenTacit and no reason to think so.
	todoLines []string
}

func (c *report) out() io.Writer {
	if c.w != nil {
		return c.w
	}
	return os.Stdout
}

// header is a section title: it goes to the report's own writer, with no
// prefix, so a caller that buffers the report (doctor --ready) captures the
// headings too rather than leaking them to a terminal it means to keep quiet.
func (c *report) header(format string, a ...any) {
	fmt.Fprintf(c.out(), format+"\n", a...)
}

// ok reports something that is already true.
func (c *report) ok(format string, a ...any) {
	fmt.Fprintf(c.out(), "   ok  "+format+"\n", a...)
}

// info states a fact that is neither good nor bad — a path, a mode, a choice.
func (c *report) info(format string, a ...any) {
	fmt.Fprintf(c.out(), "    -  "+format+"\n", a...)
}

// todo is work left for the member: something this command will not do for
// them, deliberately.
func (c *report) todo(format string, a ...any) {
	c.todos++
	msg := fmt.Sprintf(format, a...)
	c.todoLines = append(c.todoLines, msg)
	fmt.Fprintf(c.out(), " todo  %s\n", msg)
}

// warn is wrong but survivable.
func (c *report) warn(format string, a ...any) {
	c.warns++
	fmt.Fprintf(c.out(), " warn  "+format+"\n", a...)
}

// fail is wrong and decides the exit code.
func (c *report) fail(format string, a ...any) {
	c.fails++
	fmt.Fprintf(c.out(), " FAIL  "+format+"\n", a...)
}

// connectReport and checker are the same printer under the two names the
// commands already used. Kept as aliases so every call site reads as it did.
type (
	connectReport = report
	checker       = report
)
