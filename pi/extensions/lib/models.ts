// Model restriction (R16, amendment 12, brief 4-1 acceptance 6). Three layers:
// the launcher scrubs provider credentials from pi's environment and writes a
// per-seat models.json and enabledModels with only the registry entry's
// models; this module is the third: a model outside AMQ_SQUAD_MODELS is never
// selected and never sent a request.
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

type ModelLike = { provider?: string; id?: string } | undefined;

export function refOf(m: ModelLike): string {
	return m && m.provider && m.id ? `${m.provider}/${m.id}` : "";
}

export function allowed(models: string[], m: ModelLike): boolean {
	if (!models.length) return true; // no restriction configured (pi run by hand)
	return models.includes(refOf(m));
}

export function restrictModels(pi: ExtensionAPI, models: string[], note: (ctx: ExtensionContext, msg: string) => void): void {
	if (!models.length) return;
	const fallback = async (ctx: ExtensionContext): Promise<boolean> => {
		for (const ref of models) {
			const [provider, ...rest] = ref.split("/");
			const m = ctx.modelRegistry.getAll().find((x) => x.provider === provider && x.id === rest.join("/"));
			if (m && (await pi.setModel(m))) return true;
		}
		return false;
	};
	pi.on("session_start", async (_e, ctx) => {
		if (ctx.model && !allowed(models, ctx.model)) {
			if (!(await fallback(ctx))) note(ctx, `amq-squad: model ${refOf(ctx.model)} is outside this seat's registry entry (${models.join(", ")}); requests are refused`);
		}
	});
	pi.on("model_select", async (e, ctx) => {
		if (allowed(models, e.model)) return;
		note(ctx, `amq-squad: ${refOf(e.model)} is not in this seat's registry entry (${models.join(", ")}); switching back`);
		if (e.previousModel && allowed(models, e.previousModel)) await pi.setModel(e.previousModel);
		else await fallback(ctx);
	});
	pi.on("before_provider_request", (_e, ctx) => {
		if (!allowed(models, ctx.model)) {
			throw new Error(`amq-squad: refusing a request to ${refOf(ctx.model)}, which is not in this seat's registry entry`);
		}
	});
}
