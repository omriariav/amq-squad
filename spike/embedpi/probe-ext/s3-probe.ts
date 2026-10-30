// S3 post-strip probe extension. Loaded with -e; proves jiti TypeScript
// loading and runtime resolution of the pi package from the stripped tree.
import { writeFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { VERSION } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
	pi.on("agent_start", () => {
		const mark = process.env.S3_EXT_MARK;
		if (mark) writeFileSync(mark, `loaded pi ${VERSION}\n`);
	});
}
