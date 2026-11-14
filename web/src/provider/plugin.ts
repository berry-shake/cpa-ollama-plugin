import type { ProviderPlugin } from "@core/contract";
import { fmtTokens } from "@core/lib/format";
import { fmtParams, modelTags } from "./lib";

export const plugin: ProviderPlugin = {
  id: "ollama",
  name: "Ollama",
  repo: "https://github.com/berry-shake/cpa-ollama-plugin",
  models: {
    matches: (m, filter) =>
      String(m.family || "").toLowerCase().includes(filter) ||
      (Array.isArray(m.capabilities) ? m.capabilities : []).some((c) => c.toLowerCase().includes(filter)),
    // Models without "completion" are not served under "all models".
    inAll: (m) => m.chat !== false,
    hint: (t, meta) => ({ notes: meta.detail_errors ? [t("catalog.detailErrors", { n: meta.detail_errors })] : [] }),
  },
  editor: {
    init: () => ({}),
    pickName: (t, m) => ({ text: m.chat === false ? t("picker.notChat") : modelTags(t, m), title: m.family || "" }),
    // Context length and parameter size of a model, e.g. "131K context · 117B".
    modelMeta(t, m) {
      const parts: string[] = [];
      if (m.context_length) parts.push(t("picker.ctx", { ctx: fmtTokens(m.context_length) }));
      const params = fmtParams(m.parameter_size);
      if (params) parts.push(params);
      return parts.join(" · ");
    },
  },
};
