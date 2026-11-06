// Terminal-cell measurement for the lead screen. This is local on purpose:
// extensions may use pi packages as types, but must not load one at run time.

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });
const zeroWidth = /^(?:\p{Default_Ignorable_Code_Point}|\p{Control}|\p{Mark}|\p{Surrogate})+$/v;
const leadingNonPrinting = /^[\p{Default_Ignorable_Code_Point}\p{Control}\p{Format}\p{Mark}\p{Surrogate}]+/v;
const rgiEmoji = /^\p{RGI_Emoji}$/v;

function couldBeEmoji(segment: string): boolean {
	const cp = segment.codePointAt(0) ?? 0;
	return (cp >= 0x1f000 && cp <= 0x1fbff) ||
		(cp >= 0x2300 && cp <= 0x23ff) ||
		(cp >= 0x2600 && cp <= 0x27bf) ||
		segment.includes("\uFE0F") || segment.length > 2;
}

// The wide ranges used by terminals for the characters the screen can carry.
// Emoji clusters are handled above, before this code-point table.
function eastAsianWidth(cp: number): number {
	return cp >= 0x1100 && (
		cp <= 0x115f || cp === 0x2329 || cp === 0x232a ||
		(cp >= 0x2e80 && cp <= 0xa4cf && cp !== 0x303f) ||
		(cp >= 0xac00 && cp <= 0xd7a3) ||
		(cp >= 0xf900 && cp <= 0xfaff) ||
		(cp >= 0xfe10 && cp <= 0xfe19) ||
		(cp >= 0xfe30 && cp <= 0xfe6f) ||
		(cp >= 0xff00 && cp <= 0xff60) ||
		(cp >= 0xffe0 && cp <= 0xffe6) ||
		(cp >= 0x20000 && cp <= 0x3fffd)
	) ? 2 : 1;
}

function graphemeWidth(segment: string): number {
	if (segment.length === 1) {
		const code = segment.charCodeAt(0);
		if (code >= 0x20 && code <= 0x7e) return 1;
		if (code === 0x09) return 3;
	}
	if (zeroWidth.test(segment)) return 0;
	if (couldBeEmoji(segment) && rgiEmoji.test(segment)) return 2;
	const base = segment.replace(leadingNonPrinting, "");
	const cp = base.codePointAt(0);
	if (cp === undefined) return 0;
	if (cp >= 0x1f1e6 && cp <= 0x1f1ff) return 2;
	return eastAsianWidth(cp);
}

// Length of a complete CSI, OSC, or APC sequence at pos. An incomplete
// sequence is ordinary input, which mirrors pi-tui's treatment.
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
	let i = 0;
	while (i < text.length) {
		const ansi = ansiAt(text, i);
		if (ansi) {
			i += ansi.length;
			continue;
		}
		let end = i;
		while (end < text.length && !ansiAt(text, end)) end++;
		for (const { segment } of graphemes.segment(text.slice(i, end))) width += graphemeWidth(segment);
		i = end;
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

function finish(prefix: string, prefixWidth: number, ellipsis: string, ellipsisWidth: number): string {
	const reset = "\x1b[0m";
	const close = activeOsc8Close(prefix);
	return ellipsis ? `${prefix}${close}${reset}${ellipsis}${reset}` : `${prefix}${close}${reset}`;
}

function clipFragment(text: string, maxWidth: number): { text: string; width: number } {
	let result = "";
	let width = 0;
	for (const { segment } of graphemes.segment(text)) {
		const next = graphemeWidth(segment);
		if (width + next > maxWidth) break;
		result += segment;
		width += next;
	}
	return { text: result, width };
}

// Match pi-tui's default truncation contract: keep a contiguous grapheme
// prefix, reserve three columns for "...", close OSC 8 links, and reset SGR.
export function truncateToWidth(text: string, maxWidth: number, ellipsis = "..."): string {
	if (maxWidth <= 0) return "";
	if (!text) return "";
	const ellipsisWidth = visibleWidth(ellipsis);
	if (ellipsisWidth >= maxWidth) {
		if (visibleWidth(text) <= maxWidth) return text;
		const clipped = clipFragment(ellipsis, maxWidth);
		return clipped.width ? finish("", 0, clipped.text, clipped.width) : "";
	}
	if (printableAscii(text)) {
		if (text.length <= maxWidth) return text;
		const target = maxWidth - ellipsisWidth;
		return finish(text.slice(0, target), target, ellipsis, ellipsisWidth);
	}

	const target = maxWidth - ellipsisWidth;
	let result = "";
	let pendingAnsi = "";
	let visible = 0;
	let kept = 0;
	let contiguous = true;
	let overflowed = false;
	let i = 0;
	while (i < text.length) {
		const ansi = ansiAt(text, i);
		if (ansi) {
			pendingAnsi += ansi;
			i += ansi.length;
			continue;
		}
		let end = i;
		while (end < text.length && !ansiAt(text, end)) end++;
		for (const { segment } of graphemes.segment(text.slice(i, end))) {
			const width = graphemeWidth(segment);
			if (contiguous && kept + width <= target) {
				result += pendingAnsi + segment;
				pendingAnsi = "";
				kept += width;
			} else {
				contiguous = false;
				pendingAnsi = "";
			}
			visible += width;
			if (visible > maxWidth) {
				overflowed = true;
				break;
			}
		}
		if (overflowed) break;
		i = end;
	}
	if (!overflowed && i >= text.length) return text;
	return finish(result, kept, ellipsis, ellipsisWidth);
}
