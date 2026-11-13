import type { CatalogMeta, ModelView } from "../../api/types";
import { localeTag, type T } from "../../i18n";
import { allCredentials, filterModels, modelUsage } from "../../lib/credentials";
import { modelTags } from "../../lib/editor";
import { fmtParams, fmtTokens, formatWhen } from "../../lib/format";
import { store, type State } from "../../state/store";

const COLUMNS = ["col.model", "col.capabilities", "col.context", "col.thinking", "col.cpaName"];

/** The served catalog: the status's copy, else the one in the loaded list. */
export function catalogMeta(s: Pick<State, "status" | "catalog">): CatalogMeta | null {
  if (s.status && s.status.catalog) return s.status.catalog;
  return s.catalog && s.catalog.catalog ? s.catalog.catalog : null;
}

function catalogHint(t: T, meta: CatalogMeta | null, locale: string): string {
  const parts: string[] = [];
  if (meta && meta.fetched_at) {
    parts.push(t("catalog.summary", { count: meta.count || 0, time: formatWhen(meta.fetched_at, locale) }));
    if (meta.checked_at && meta.checked_at !== meta.fetched_at) parts.push(t("catalog.checked", { time: formatWhen(meta.checked_at, locale) }));
  }
  if (meta && meta.detail_errors) parts.push(t("catalog.detailErrors", { n: meta.detail_errors }));
  return parts.join(" · ");
}

// Named levels with the default marked, or on / off for toggle-only models.
function ThinkingCell({ t, m }: { t: T; m: ModelView }) {
  const levels = Array.isArray(m.thinking_levels) ? m.thinking_levels : [];
  if (levels.length) {
    return (
      <td>
        <div>{levels.join(" / ")}</div>
        {m.thinking_default ? <div className="sub">{t("catalog.thinkingDefault", { v: m.thinking_default })}</div> : null}
      </td>
    );
  }
  if (m.thinking_toggle) {
    const on = m.thinking_default === "true";
    return (
      <td className="nowrap">
        <div>{t("catalog.thinkingToggle")}</div>
        {m.thinking_default ? <div className="sub">{t("catalog.thinkingDefault", { v: t(on ? "catalog.thinkingOn" : "catalog.thinkingOff") })}</div> : null}
      </td>
    );
  }
  return <td className="nowrap"><span className="sub">—</span></td>;
}

function CatalogTable({ t, s }: { t: T; s: State }) {
  const models = s.catalog && Array.isArray(s.catalog.models) ? s.catalog.models : [];
  if (models.length === 0) return <div className="empty">{t("catalog.empty")}</div>;
  const visible = filterModels(models, s.catalogFilter);
  if (visible.length === 0) return <div className="empty">{t("picker.noMatch")}</div>;
  const usage = modelUsage(allCredentials(s.status), models);
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>{COLUMNS.map((key) => <th key={key}>{t(key)}</th>)}</tr>
        </thead>
        <tbody>
          {visible.map((m) => {
            const used = usage.get(m.id);
            return (
              <tr key={m.id}>
                <td>
                  <div className="mono strong">{m.id}</div>
                  {m.family ? <div className="sub">{m.family}</div> : null}
                </td>
                <td>
                  <div>{modelTags(t, m) || "—"}</div>
                  {m.chat === false ? <div className="sub warn-text">{t("catalog.notChat")}</div> : null}
                </td>
                <td className="nowrap">
                  <div>{m.context_length ? fmtTokens(m.context_length) : "—"}</div>
                  {m.parameter_size || m.quantization ? (
                    <div className="sub">{[fmtParams(m.parameter_size), m.quantization || ""].filter(Boolean).join(" · ")}</div>
                  ) : null}
                </td>
                <ThinkingCell t={t} m={m} />
                <td>
                  {used ? (
                    <>
                      {[...used.names].sort().map((name) => <div key={name} className="mono">{name}</div>)}
                      <div className="sub">{t("catalog.usedBy", { count: used.keys })}</div>
                    </>
                  ) : (
                    <span className="sub">{t("catalog.unused")}</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function ModelsPanel({ t, s }: { t: T; s: State }) {
  const meta = catalogMeta(s);
  const locale = localeTag(s.lang);
  const models = s.catalog && Array.isArray(s.catalog.models) ? s.catalog.models : [];
  let count = "";
  if (s.catalog && models.length) {
    const filter = s.catalogFilter.trim();
    count = filter
      ? t("catalog.shown", { shown: filterModels(models, filter).length, total: models.length })
      : t("catalog.count", { count: models.length });
  }
  const failed = !!s.catalogError && !s.catalogLoading;
  return (
    <section className="panel" role="tabpanel" id="panel-models" aria-labelledby="tab-models" hidden={s.tab !== "models"}>
      <section className="card">
        <div className="card-header">
          <div className="card-title">
            <h2>{t("catalog.title")}</h2>
            <span className="hint" id="catalog-hint">{catalogHint(t, meta, locale)}</span>
          </div>
          <div className="actions">
            <button id="catalog-button" className="btn btn-secondary btn-sm" type="button" disabled={!!s.busy.catalog} onClick={() => void store.reloadCatalog()}>
              {t("actions.reloadCatalog")}
            </button>
          </div>
        </div>
        <div className="toolbar">
          <input
            className="input input-sm search"
            id="catalog-search"
            type="search"
            spellCheck={false}
            placeholder={t("catalog.search")}
            value={s.catalogFilter}
            onChange={(event) => store.setCatalogFilter(event.target.value)}
          />
          <span className="count" id="catalog-count">{count}</span>
        </div>
        <div id="catalog-body">
          {meta && meta.last_error ? (
            <div className="notice catalog-notice" data-state="error">
              <span className="notice-text">{t("catalog.syncError", { msg: meta.last_error })}</span>
            </div>
          ) : null}
          {s.catalog ? (
            <CatalogTable t={t} s={s} />
          ) : (
            <div className={"empty" + (failed ? " danger-text" : "")}>
              {failed ? t("picker.error", { msg: s.catalogError || "" }) : s.catalogLoading ? t("picker.loading") : t("keys.notLoaded")}
            </div>
          )}
        </div>
      </section>
    </section>
  );
}
