// Terminal-cell measurement for the lead screen. This is local on purpose:
// extensions may use pi packages as types, but must not load one at run time.
//
// The measure is deliberately an upper bound, not a copy of pi-tui's Unicode
// tables. The property row checks that bound against the installed pi-tui
// 1.3.0 pin, so a dependency bump reruns the compatibility check.

function codePointWidth(cp: number): number {
	if (cp === 0x09) return 3;
	if (cp >= 0x20 && cp <= 0x7e) return 1;
	// This is intentionally the whole allowlist. Every entry is covered by the
	// property test against the installed pi-tui.
	if (cp >= 0x00c0 && cp <= 0x00ff && cp !== 0x00d7 && cp !== 0x00f7) return 1;
	if (cp >= 0x05d0 && cp <= 0x05ea) return 1;
	// Count per code point, including marks, controls, and lone surrogates. Do
	// not segment across escapes: pi-tui strips first and may form a grapheme
	// from both sides, while this remains an upper bound.
	return 2;
}

function plainWidth(text: string): number {
	let width = 0;
	for (const char of text) width += codePointWidth(char.codePointAt(0) ?? 0);
	return width;
}

// Length of the same complete CSI, OSC, or APC sequence pi-tui skips at pos.
// An unsupported CSI final byte is text for both measures.
function ansiLength(text: string, pos: number): number {
	if (text.charCodeAt(pos) !== 0x1b) return 0;
	const next = text[pos + 1];
	if (next === "[") {
		for (let i = pos + 2; i < text.length; i++) {
			const c = text.charCodeAt(i);
			if (c === 0x6d || c === 0x47 || c === 0x4b || c === 0x48 || c === 0x4a) return i + 1 - pos;
		}
		return 0;
	}
	if (next === "]" || next === "_") {
		for (let i = pos + 2; i < text.length; i++) {
			if (text.charCodeAt(i) === 0x07) return i + 1 - pos;
			if (text.charCodeAt(i) === 0x1b && text[i + 1] === "\\") return i + 2 - pos;
		}
	}
	return 0;
}

// Length of an ESC-led run for truncation. Unlike ansiLength, this also sees
// unsupported and incomplete runs: they keep their text width, but a cut moves
// to before ESC instead of emitting a prefix that could become an escape.
function escapeRunLength(text: string, pos: number): number {
	if (text.charCodeAt(pos) !== 0x1b) return 0;
	const next = text[pos + 1];
	if (next === "[") {
		let i = pos + 2;
		while (i < text.length) {
			const c = text.charCodeAt(i);
			if (c < 0x30 || c > 0x3f) break;
			i++;
		}
		while (i < text.length) {
			const c = text.charCodeAt(i);
			if (c < 0x20 || c > 0x2f) break;
			i++;
		}
		if (i < text.length) {
			const c = text.charCodeAt(i);
			if (c >= 0x40 && c <= 0x7e) return i + 1 - pos;
		}
		return text.length - pos;
	}
	if (next === "]" || next === "_" || next === "P" || next === "X" || next === "^") {
		for (let i = pos + 2; i < text.length; i++) {
			if (text.charCodeAt(i) === 0x07) return i + 1 - pos;
			if (text.charCodeAt(i) === 0x1b && text[i + 1] === "\\") return i + 2 - pos;
		}
		return text.length - pos;
	}
	let i = pos + 1;
	while (i < text.length) {
		const c = text.charCodeAt(i);
		if (c < 0x20 || c > 0x2f) break;
		i++;
	}
	if (i < text.length) {
		const c = text.charCodeAt(i);
		if (c >= 0x30 && c <= 0x7e) return i + 1 - pos;
	}
	return text.length - pos;
}

type WidthUnit = { text: string; width: number; ansi: boolean };

// The carry's measure run. It deliberately recognizes fewer ESC-led runs
// than escapeRunLength: the latter is a cut boundary, not a width rule.
function measureRunLength(text: string, pos: number): number {
	if (text.charCodeAt(pos) !== 0x1b) return 0;
	const next = text[pos + 1];
	if (next === "[") {
		let i = pos + 2;
		while (i < text.length) {
			const c = text.charCodeAt(i);
			if (c < 0x30 || c > 0x3f) break;
			i++;
		}
		while (i < text.length) {
			const c = text.charCodeAt(i);
			if (c < 0x20 || c > 0x2f) break;
			i++;
		}
		if (i < text.length) {
			const c = text.charCodeAt(i);
			if (c >= 0x40 && c <= 0x7e) return i + 1 - pos;
		}
		return text.length - pos;
	}
	if (next === "]" || next === "_") {
		for (let i = pos + 2; i < text.length; i++) {
			if (text.charCodeAt(i) === 0x07) return i + 1 - pos;
			if (text.charCodeAt(i) === 0x1b && text[i + 1] === "\\") return i + 2 - pos;
		}
		return text.length - pos;
	}
	return 0;
}

function measureUnitAt(text: string, pos: number): WidthUnit {
	const ansi = ansiLength(text, pos);
	if (ansi) return { text: text.slice(pos, pos + ansi), width: 0, ansi: true };
	const run = measureRunLength(text, pos);
	if (run) {
		const value = text.slice(pos, pos + run);
		return { text: value, width: plainWidth(value), ansi: false };
	}
	const cp = text.codePointAt(pos) ?? 0;
	const length = cp > 0xffff ? 2 : 1;
	return { text: text.slice(pos, pos + length), width: codePointWidth(cp), ansi: false };
}

function cutRunLength(text: string, pos: number): number {
	if (text.charCodeAt(pos) !== 0x1b) return 0;
	// The last ESC decides the run. Each earlier ESC followed by ESC is a
	// one-code-unit measure unit of width 2; allowing a cut between them would
	// leave a lone ESC before the sequence begun by the next one. Treat the
	// chain and the run begun by its last ESC as one cut unit.
	let last = pos;
	while (text.charCodeAt(last + 1) === 0x1b) last++;
	if (last > pos) {
		const suffix = ansiLength(text, last) || escapeRunLength(text, last);
		return last - pos + suffix;
	}
	return escapeRunLength(text, pos);
}

function cutUnitAt(text: string, pos: number): WidthUnit {
	const measured = measureUnitAt(text, pos);
	if (measured.ansi || text.charCodeAt(pos) !== 0x1b) return measured;
	const run = cutRunLength(text, pos);
	if (!run) return measured;
	let end = pos + run;
	let width = 0;
	// A measure unit may begin inside this run and end after it, as in T05.
	// Extend through that unit so cut units partition the string and their
	// widths still add up to visibleWidth.
	for (let i = pos; i < end;) {
		const unit = measureUnitAt(text, i);
		width += unit.width;
		i += unit.text.length;
		if (i > end) end = i;
	}
	return { text: text.slice(pos, end), width, ansi: false };
}

function ansiAt(text: string, pos: number): string {
	const length = ansiLength(text, pos);
	return length ? text.slice(pos, pos + length) : "";
}

function printableAscii(text: string): boolean {
	for (let i = 0; i < text.length; i++) {
		const code = text.charCodeAt(i);
		if (code < 0x20 || code > 0x7e) return false;
	}
	return true;
}

export function visibleWidth(text: string): number {
	let width = 0;
	for (let i = 0; i < text.length;) {
		const unit = measureUnitAt(text, i);
		width += unit.width;
		i += unit.text.length;
	}
	return width;
}

function activeOsc8Close(prefix: string): string {
	let close = "";
	let i = 0;
	while (i < prefix.length) {
		const ansi = ansiAt(prefix, i);
		if (!ansi) {
			i++;
			continue;
		}
		if (ansi.startsWith("\x1b]8;")) {
			const terminator = ansi.endsWith("\x07") ? "\x07" : "\x1b\\";
			const body = ansi.slice(4, terminator === "\x07" ? -1 : -2);
			const semicolon = body.indexOf(";");
			if (semicolon >= 0) close = body.slice(semicolon + 1) ? `\x1b]8;;${terminator}` : "";
		}
		i += ansi.length;
	}
	return close;
}

function finish(prefix: string, ellipsis: string): string {
	const reset = "\x1b[0m";
	const close = activeOsc8Close(prefix);
	return ellipsis ? `${prefix}${close}${reset}${ellipsis}${reset}` : `${prefix}${close}${reset}`;
}

function clipFragment(text: string, maxWidth: number): { text: string; width: number } {
	let result = "";
	let pendingAnsi = "";
	let width = 0;
	for (let i = 0; i < text.length;) {
		const unit = measureUnitAt(text, i);
		i += unit.text.length;
		if (unit.ansi) {
			pendingAnsi += unit.text;
			continue;
		}
		if (width + unit.width > maxWidth) break;
		result += pendingAnsi + unit.text;
		pendingAnsi = "";
		width += unit.width;
	}
	return { text: result, width };
}

// Keep a contiguous code-point prefix, reserve the ellipsis, close OSC 8
// links, and reset SGR. ESC-led runs are atomic even when pi-tui treats them as
// visible text, so truncation never manufactures a partial control sequence.
export function truncateToWidth(text: string, maxWidth: number, ellipsis = "..."): string {
	if (maxWidth <= 0 || !text) return "";
	const textWidth = visibleWidth(text);
	if (textWidth <= maxWidth) return text;
	const ellipsisWidth = visibleWidth(ellipsis);
	if (ellipsisWidth >= maxWidth) {
		const clipped = clipFragment(ellipsis, maxWidth);
		return clipped.width ? finish("", clipped.text) : "";
	}
	if (printableAscii(text)) {
		const target = maxWidth - ellipsisWidth;
		return finish(text.slice(0, target), ellipsis);
	}

	const target = maxWidth - ellipsisWidth;
	let result = "";
	let pendingAnsi = "";
	let kept = 0;
	for (let i = 0; i < text.length;) {
		const unit = cutUnitAt(text, i);
		i += unit.text.length;
		if (unit.ansi) {
			pendingAnsi += unit.text;
			continue;
		}
		if (kept + unit.width > target) break;
		result += pendingAnsi + unit.text;
		pendingAnsi = "";
		kept += unit.width;
	}
	return finish(result, ellipsis);
}
