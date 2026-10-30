// The team lead's read-only view of the repository (amendment 26): a file
// read and a directory list scoped to the repository root. The lead has no
// built-in read, ls or bash tool; anything that runs a command is a seat's
// task. Nothing is written.
//
// A path is validated with realpath (it must be the root or inside it),
// then opened, and the opened descriptor is checked against an lstat walk of
// the validated components: no component may be a symlink and the final
// component must have the descriptor's device and inode. A swap between the
// validation and the open is refused (amendment 26 review). This is best
// effort within the same-UID limit: a same-user writer can still swap a
// component between the walk and the read of a directory listing.
import * as fs from "node:fs";
import * as path from "node:path";

const MAX_READ = 256 * 1024;
const MAX_ENTRIES = 1000;

export function resolveInRoot(root: string, p: string | undefined): { realRoot: string; target: string } {
	const realRoot = fs.realpathSync(root);
	const target = fs.realpathSync(path.resolve(realRoot, p && p.length ? p : "."));
	if (target !== realRoot && !target.startsWith(realRoot + path.sep)) {
		throw new Error(`${p} is outside the repository root ${realRoot}`);
	}
	return { realRoot, target };
}

// openVerified opens target and proves the descriptor is the file the
// validated path names: every component from the root down is a real
// directory (lstat, no symlink) and the last one has the descriptor's
// device and inode.
export function openVerified(realRoot: string, target: string): { fd: number; st: fs.Stats } {
	const fd = fs.openSync(target, "r");
	try {
		const st = fs.fstatSync(fd);
		const rel = path.relative(realRoot, target);
		let cur = realRoot;
		let last = fs.lstatSync(cur);
		for (const part of rel ? rel.split(path.sep) : []) {
			if (!last.isDirectory() || last.isSymbolicLink()) throw new Error(`${cur} is not a real directory`);
			cur = path.join(cur, part);
			last = fs.lstatSync(cur);
			if (last.isSymbolicLink()) throw new Error(`${cur} became a symlink after validation`);
		}
		if (last.dev !== st.dev || last.ino !== st.ino) throw new Error(`${target} changed between validation and open`);
		return { fd, st };
	} catch (err) {
		fs.closeSync(fd);
		throw err;
	}
}

export function repoRead(root: string, p: string, offset = 1, limit = 2000): string {
	const { realRoot, target } = resolveInRoot(root, p);
	const { fd, st } = openVerified(realRoot, target);
	try {
		if (!st.isFile()) throw new Error(`${p} is not a regular file`);
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
	const { realRoot, target } = resolveInRoot(root, p);
	const { fd, st } = openVerified(realRoot, target);
	try {
		if (!st.isDirectory()) throw new Error(`${p} is not a directory`);
		const ents = fs.readdirSync(target, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name));
		// The listing was read by path: re-check that the path still names the
		// directory that was opened and verified.
		const again = fs.lstatSync(target);
		if (again.dev !== st.dev || again.ino !== st.ino) throw new Error(`${target} changed while it was listed`);
		const lines = ents.slice(0, MAX_ENTRIES).map((d) => (d.isDirectory() ? d.name + "/" : d.isSymbolicLink() ? d.name + "@" : d.name));
		if (ents.length > MAX_ENTRIES) lines.push(`[${ents.length - MAX_ENTRIES} more]`);
		return lines.join("\n") || "(empty)";
	} finally {
		fs.closeSync(fd);
	}
}
