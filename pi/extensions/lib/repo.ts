// The team lead's read-only view of the repository (amendment 26): a file
// read and a directory list scoped to the repository root. The lead has no
// built-in read, ls or bash tool; anything that runs a command is a seat's
// task. A path is resolved with realpath, so a symlink that leaves the root
// is refused, and nothing is written.
import * as fs from "node:fs";
import * as path from "node:path";

const MAX_READ = 256 * 1024;
const MAX_ENTRIES = 1000;

export function resolveInRoot(root: string, p: string | undefined): string {
	const realRoot = fs.realpathSync(root);
	const target = fs.realpathSync(path.resolve(realRoot, p && p.length ? p : "."));
	if (target !== realRoot && !target.startsWith(realRoot + path.sep)) {
		throw new Error(`${p} is outside the repository root ${realRoot}`);
	}
	return target;
}

export function repoRead(root: string, p: string, offset = 1, limit = 2000): string {
	const target = resolveInRoot(root, p);
	const st = fs.statSync(target);
	if (!st.isFile()) throw new Error(`${p} is not a regular file`);
	const fd = fs.openSync(target, "r");
	try {
		const buf = Buffer.alloc(Math.min(st.size, MAX_READ));
		const n = fs.readSync(fd, buf, 0, buf.length, 0);
		const lines = buf.subarray(0, n).toString("utf8").split("\n");
		const start = Math.max(1, Math.floor(offset)) - 1;
		const out = lines.slice(start, start + Math.max(1, Math.floor(limit)));
		const more = st.size > MAX_READ ? `\n[truncated at ${MAX_READ} bytes of ${st.size}]` : "";
		return out.join("\n") + more;
	} finally {
		fs.closeSync(fd);
	}
}

export function repoList(root: string, p?: string): string {
	const target = resolveInRoot(root, p);
	if (!fs.statSync(target).isDirectory()) throw new Error(`${p} is not a directory`);
	const ents = fs.readdirSync(target, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name));
	const lines = ents.slice(0, MAX_ENTRIES).map((d) => (d.isDirectory() ? d.name + "/" : d.isSymbolicLink() ? d.name + "@" : d.name));
	if (ents.length > MAX_ENTRIES) lines.push(`[${ents.length - MAX_ENTRIES} more]`);
	return lines.join("\n") || "(empty)";
}
