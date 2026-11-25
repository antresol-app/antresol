// «Расширения» (admin): the installed addons — name, description, version
// («Версия 0.4.9 → 0.5.0» when there is a newer release), state — each with its switch, «Обновить» when
// there is a newer release, and «⋯» with «Журнал» and «Удалить» (after a
// confirmation, «Удалить и данные» optional). «Добавить расширение» takes
// a GitHub link («Проверить обновления» asks GitHub again at once), shows what the release is and asks for (with the warning:
// an addon runs with the server's rights) and installs that very release;
// «Обновить» shows the new release the same way first. Every change loads
// this tab's addons again, so their tiles, actions and pages come and go
// at once.
// A pinned addon (installed by the deployment) says «Закреплено при
// развёртывании» and has no «Обновить» or «Удалить»: only its switch and
// «Журнал»; one whose pin failed at boot says so and cannot be switched
// on.
import { useCallback, useEffect, useRef, useState } from "react";
import { APP_NAME } from "../../brand";
import {
  errorText,
  getAddonLog,
  getAddons,
  installAddon,
  previewAddon,
  removeAddon,
  setAddonEnabled,
  updateAddon,
} from "../../api/client";
import type { AddonInfo, AddonPreview } from "../../api/types";
import { useAddonWebFailure } from "../../addons/registry";
import { Icon } from "../../components/Icon";
import { t as tr, useT, type Key } from "../../i18n";
import { Button } from "../../ui/Button";
import { IconButton } from "../../ui/IconButton";
import { GroupedSection, ListRow } from "../../ui/List";
import { Menu, MenuItem, MenuSeparator } from "../../ui/Menu";
import { Notice } from "../../ui/Notice";
import { Sheet } from "../../ui/Sheet";
import { TextField } from "../../ui/TextField";
import { Toggle } from "../../ui/Toggle";
import { Spinner } from "../../ui/Spinner";
import m from "./Manage.module.css";
import styles from "./Addons.module.css";

type State =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; list: AddonInfo[] };

/** How often an addon that is starting is asked again. */
const STARTING_POLL_MS = 1000;
const CHECKING_POLL_MS = 3000;

/** The slots of this tab follow the change (the loader is its own chunk). */
export function reloadSlots() {
  void import("../../addons/loader")
    .then((l) => l.reloadAddons())
    .catch((e: unknown) => console.warn("antresol: addon reload failed", e));
}

/** Its web part did not run in this tab (addons/loader.ts): said on its row instead of only in the console. */
function WebFailure({ id }: { id: string }) {
  const t = useT();
  const why = useAddonWebFailure(id);
  if (!why) return null;
  return (
    <>
      <br />
      <span className={styles.pinFailed} role="status">
        {t("addons.webFailed", { reason: why })}
      </span>
    </>
  );
}

/** A permission in words; an unknown one as the addon named it. */
function permissionText(p: string): string {
  const key = `addons.permission.${p}` as Key;
  const s = tr(key);
  return s === key ? p : s;
}

export function Addons() {
  const t = useT();
  const [state, setState] = useState<State>({ status: "loading" });
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<AddonInfo | null>(null);
  const [updating, setUpdating] = useState<AddonInfo | null>(null);
  const [log, setLog] = useState<AddonInfo | null>(null);

  // Every load and every change of the list bumps it: an answer that
  // arrives after a newer one (or after a change) is dropped.
  const version = useRef(0);
  // A quiet load (the poll) that failed: the list stays as it is and the
  // poll goes on (this counter re-arms it).
  const [pollFailed, setPollFailed] = useState(0);
  const load = useCallback((quiet = false, fresh = false) => {
    const mine = ++version.current;
    if (!quiet) setState({ status: "loading" });
    getAddons(fresh).then(
      (list) => {
        if (mine === version.current) setState({ status: "ready", list });
      },
      (e: unknown) => {
        if (mine !== version.current) return;
        if (quiet) {
          console.warn("antresol: addons poll failed", e);
          setPollFailed((n) => n + 1);
          return;
        }
        setState({
          status: "error",
          message: errorText(e, tr("addons.loadFailed")),
        });
      },
    );
  }, []);
  useEffect(() => {
    document.title = `${tr("addons.title")} — ${APP_NAME}`;
    load();
  }, [load]);
  // An addon still starting (an import before it serves, say) is asked
  // again until it is not; once one runs, its slots load. An update check
  // the server is still running (GitHub slow) is asked again too, slower.
  const starting = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (state.status !== "ready") return;
    const now = new Set(state.list.filter((a) => a.status === "starting").map((a) => a.id));
    const started = [...starting.current].some((id) => !now.has(id));
    starting.current = now;
    if (started) reloadSlots();
    const checking = state.list.some((a) => a.updateState === "checking");
    if (now.size === 0 && !checking) return;
    const timer = window.setTimeout(() => load(true), now.size > 0 ? STARTING_POLL_MS : CHECKING_POLL_MS);
    return () => window.clearTimeout(timer);
  }, [state, load, pollFailed]);

  const put = (a: AddonInfo) => {
    version.current++;
    setState((s) =>
      s.status !== "ready"
        ? s
        : {
            ...s,
            list: s.list.some((x) => x.id === a.id)
              ? s.list.map((x) => (x.id === a.id ? a : x))
              : [...s.list, a],
          },
    );
  };
  const drop = (id: string) => {
    version.current++;
    setState((s) =>
      s.status === "ready"
        ? { ...s, list: s.list.filter((x) => x.id !== id) }
        : s,
    );
  };
  const mark = (id: string, on: boolean) =>
    setBusy((b) => {
      const n = new Set(b);
      if (on) n.add(id);
      else n.delete(id);
      return n;
    });

  /** Runs a change of addon `a` (its row busy meanwhile); the slots follow.
   * A failed one loads the list again: the server may have done part of it. */
  const change = async (a: AddonInfo, run: () => Promise<void>) => {
    setProblem("");
    mark(a.id, true);
    try {
      await run();
      reloadSlots();
    } catch (e) {
      setProblem(errorText(e, tr("addons.failed")));
      load(true);
    } finally {
      mark(a.id, false);
    }
  };
  const toggle = (a: AddonInfo, on: boolean) =>
    change(a, async () => {
      put({ ...a, enabled: on });
      put(await setAddonEnabled(a.id, on));
    });
  const remove = (a: AddonInfo, data: boolean) =>
    change(a, async () => {
      await removeAddon(a.id, data);
      drop(a.id);
    });

  const list = state.status === "ready" ? state.list : [];
  return (
    <section aria-labelledby="addons-title">
      <header className={m.header}>
        <div>
          <h2 id="addons-title" className={m.title}>
            {t("addons.title")}
          </h2>
          <p className={m.subtitle}>{t("addons.subtitle")}</p>
        </div>
        <div className={m.headerActions}>
          {list.some((a) => !a.pinned) && (
            <Button
              variant="secondary"
              icon="restart"
              loading={state.status === "ready" && list.some((a) => a.updateState === "checking")}
              onClick={() => load(true, true)}
            >
              {t("addons.checkUpdates")}
            </Button>
          )}
          <Button icon="plus" onClick={() => setAdding(true)}>
            {t("addons.add")}
          </Button>
        </div>
      </header>
      {problem && (
        <div className={m.problem} role="alert">
          {problem}
        </div>
      )}
      {state.status === "loading" && (
        <div className={m.spinnerRow} role="status">
          <Spinner track className={m.spinner} /> {t("addons.loading")}
        </div>
      )}
      {state.status === "error" && (
        <Notice
          tone="destructive"
          actions={
            <Button size="small" variant="secondary" onClick={() => load()}>
              {t("common.retry")}
            </Button>
          }
        >
          {state.message}
        </Notice>
      )}
      {state.status === "ready" && list.length === 0 && (
        <div className={m.empty}>
          <Icon name="puzzle" size={40} strokeWidth={1.4} />
          <h3>{t("addons.emptyTitle")}</h3>
          <p>{t("addons.emptyText")}</p>
        </div>
      )}
      {list.length > 0 && (
        <GroupedSection>
          {list.map((a) => (
            <ListRow
              key={a.id}
              leading={
                <span className={styles.glyph} aria-hidden>
                  <Icon name="puzzle" size={16} strokeWidth={2} />
                </span>
              }
              label={a.name}
              hint={a.description || undefined}
              status={
                <>
                  {/* a newer release right in the version: «Версия 0.4.9 → 0.5.0», with «Обновить» beside */}
                  {a.update && !a.pinned ? (
                    <span className={styles.update}>
                      {t("addons.versionUpdate", { version: a.version, update: a.update })}
                    </span>
                  ) : (
                    t("addons.version", { version: a.version })
                  )}{" "}
                  · {t(`addons.status.${a.status}` as Key)}
                  {a.enabled && a.status === "running" && <WebFailure id={a.id} />}
                  {a.status === "incompatible" && a.minCore && (
                    <>
                      <br />
                      <span className={styles.needsCore}>{t("addons.needsCore", { version: a.minCore })}</span>
                    </>
                  )}
                  {a.pinned && (
                    <>
                      <br />
                      <span>{t("addons.pinned")}</span>
                      {a.pinFailed && (
                        <>
                          <br />
                          <span className={styles.pinFailed}>{t("addons.pinFailed")}</span>
                        </>
                      )}
                    </>
                  )}
                  {!a.update && !a.pinned && a.updateState === "checking" && (
                    <>
                      <br />
                      <span>{t("addons.updateChecking")}</span>
                    </>
                  )}
                  {!a.update && !a.pinned && (a.updateState === "failed" || a.updateState === "needsCore") && (
                    <>
                      <br />
                      <span className={styles.checkProblem}>
                        {a.updateState === "needsCore"
                          ? a.updateError
                          : t("addons.updateCheckFailed", { reason: a.updateError ?? "" })}
                      </span>
                    </>
                  )}
                </>
              }
            >
              <div className={styles.controls}>
                {!a.update && !a.pinned && a.updateState === "failed" && (
                  <Button
                    size="small"
                    variant="secondary"
                    aria-label={t("updates.recheckNamed", { name: a.name })}
                    onClick={() => load(true, true)}
                  >
                    {t("addons.recheck")}
                  </Button>
                )}
                {a.update && !a.pinned && (
                  <Button
                    size="small"
                    variant="secondary"
                    aria-label={t("addons.updateNamed", { name: a.name })}
                    loading={busy.has(a.id)}
                    onClick={() => setUpdating(a)}
                  >
                    {t("addons.update")}
                  </Button>
                )}
                <Toggle
                  label={a.name}
                  checked={a.enabled}
                  disabled={busy.has(a.id) || (a.pinFailed && !a.enabled)}
                  onChange={(on) => void toggle(a, on)}
                />
                <Menu
                  label={t("addons.more", { name: a.name })}
                  placement="bottom-end"
                  trigger={(p) => (
                    <IconButton
                      {...p}
                      size="sm"
                      icon="ellipsis"
                      label={t("addons.more", { name: a.name })}
                    />
                  )}
                >
                  {(close) => (
                    <>
                      <MenuItem
                        icon={<Icon name="list" size={18} />}
                        label={t("addons.log")}
                        onSelect={() => {
                          close(false);
                          setLog(a);
                        }}
                      />
                      {!a.pinned && (
                        <>
                          <MenuSeparator />
                          <MenuItem
                            destructive
                            icon={<Icon name="trash" size={18} />}
                            label={t("addons.remove")}
                            disabled={busy.has(a.id)}
                            onSelect={() => {
                              close(false);
                              setRemoving(a);
                            }}
                          />
                        </>
                      )}
                    </>
                  )}
                </Menu>
              </div>
            </ListRow>
          ))}
        </GroupedSection>
      )}
      {adding && (
        <AddSheet
          onClose={() => setAdding(false)}
          onInstalled={(a) => {
            setAdding(false);
            put(a);
            reloadSlots();
          }}
        />
      )}
      {updating && (
        <AddSheet
          updating={updating}
          onClose={() => setUpdating(null)}
          onInstalled={(a) => {
            setUpdating(null);
            put(a);
            reloadSlots();
          }}
        />
      )}
      {removing && (
        <RemoveSheet
          addon={removing}
          onClose={() => setRemoving(null)}
          onRemove={(data) => {
            const a = removing;
            setRemoving(null);
            void remove(a, data);
          }}
        />
      )}
      {log && <LogSheet addon={log} onClose={() => setLog(null)} />}
    </section>
  );
}

/**
 * «Добавить расширение»: a link, then what its latest release is and asks
 * for, then the install of that very version. With `updating`, the same
 * preview of an installed addon's newer release before «Обновить».
 */
export function AddSheet({
  updating,
  onClose,
  onInstalled,
}: {
  updating?: AddonInfo;
  onClose: () => void;
  onInstalled: (a: AddonInfo) => void;
}) {
  const t = useT();
  const [url, setUrl] = useState(updating?.source ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [preview, setPreview] = useState<AddonPreview | null>(null);

  const check = useCallback(async (source: string) => {
    setBusy(true);
    setError("");
    try {
      setPreview(await previewAddon(source.trim()));
    } catch (e) {
      setError(errorText(e, tr("addons.checkFailed")));
    } finally {
      setBusy(false);
    }
  }, []);
  // an update shows its release at once
  const source = updating?.source;
  useEffect(() => {
    if (source) void check(source);
  }, [source, check]);
  const install = async (p: AddonPreview) => {
    setBusy(true);
    setError("");
    try {
      onInstalled(
        updating
          ? await updateAddon(updating.id, p.version)
          : await installAddon(url.trim(), p.version),
      );
    } catch (e) {
      setError(
        errorText(
          e,
          t(updating ? "addons.updateFailed" : "addons.installFailed"),
        ),
      );
      setBusy(false);
    }
  };

  return (
    <Sheet
      title={
        updating
          ? t("addons.updateNamed", { name: updating.name })
          : t("addons.add")
      }
      icon={<Icon name="puzzle" size={30} strokeWidth={1.8} />}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t("common.cancel"), kind: "cancel", run: onClose },
        preview
          ? {
              label: updating
                ? t("addons.update")
                : preview.installed && preview.installed !== preview.version
                  ? t("addons.reinstall", { version: preview.version })
                  : t("addons.install"),
              kind: "primary",
              disabled: busy,
              run: () => void install(preview),
            }
          : {
              label: t("addons.check"),
              kind: "primary",
              disabled: busy || !url.trim(),
              run: () => void check(url),
            },
      ]}
    >
      <div className={styles.form}>
        {updating ? (
          !preview &&
          error && (
            <p className={styles.error} role="alert">
              {error}
            </p>
          )
        ) : (
          <TextField
            label={t("addons.urlLabel")}
            hint={t("addons.urlHint")}
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            value={url}
            readOnly={!!preview}
            error={!preview && error ? error : null}
            onChange={(e) => setUrl(e.target.value)}
          />
        )}
        {preview && (
          <div className={styles.preview}>
            <p className={styles.previewName}>
              {preview.name}{" "}
              <span className={styles.previewVersion}>{preview.version}</span>
            </p>
            {preview.author && (
              <p className={styles.previewMeta}>
                {t("addons.author", { author: preview.author })}
              </p>
            )}
            {preview.installed && (
              <p className={styles.previewMeta}>
                {t("addons.installedAlready", { version: preview.installed })}
              </p>
            )}
            {preview.description && (
              <p className={styles.previewText}>{preview.description}</p>
            )}
            <p className={styles.permTitle}>{t("addons.permissions")}</p>
            {preview.permissions.length > 0 ? (
              <ul className={styles.perms}>
                {preview.permissions.map((p) => (
                  <li key={p}>{permissionText(p)}</li>
                ))}
              </ul>
            ) : (
              <p className={styles.previewMeta}>{t("addons.noPermissions")}</p>
            )}
            <Notice>{t("addons.warning")}</Notice>
            {error && (
              <p className={styles.error} role="alert">
                {error}
              </p>
            )}
          </div>
        )}
      </div>
    </Sheet>
  );
}

function RemoveSheet({
  addon,
  onClose,
  onRemove,
}: {
  addon: AddonInfo;
  onClose: () => void;
  onRemove: (data: boolean) => void;
}) {
  const t = useT();
  const [data, setData] = useState(false);
  return (
    <Sheet
      title={t("addons.removeTitle", { name: addon.name })}
      message={t("addons.removeMessage")}
      icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
      onCancel={onClose}
      actions={[
        { label: t("common.cancel"), kind: "cancel", run: onClose },
        {
          label: t("addons.remove"),
          kind: "destructive",
          run: () => onRemove(data),
        },
      ]}
    >
      <div className={styles.form}>
        <GroupedSection>
          <ListRow label={t("addons.removeData")}>
            <Toggle
              label={t("addons.removeData")}
              checked={data}
              onChange={setData}
            />
          </ListRow>
        </GroupedSection>
      </div>
    </Sheet>
  );
}

function LogSheet({
  addon,
  onClose,
}: {
  addon: AddonInfo;
  onClose: () => void;
}) {
  const t = useT();
  const [lines, setLines] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    getAddonLog(addon.id).then(
      (l) => live && setLines(l),
      (e: unknown) => live && setError(errorText(e, tr("addons.logFailed"))),
    );
    return () => {
      live = false;
    };
  }, [addon.id]);
  return (
    <Sheet
      title={t("addons.logTitle", { name: addon.name })}
      role="dialog"
      size="large"
      dismissOnOutside
      onCancel={onClose}
    >
      {error ? (
        <p className={styles.error} role="alert">
          {error}
        </p>
      ) : lines === null ? (
        <div
          className={m.spinnerRow}
          role="status"
          aria-label={t("common.loading")}
        >
          <Spinner track className={m.spinner} />
        </div>
      ) : lines.length === 0 ? (
        <p className={styles.previewMeta}>{t("addons.logEmpty")}</p>
      ) : (
        <pre className={styles.log}>{lines.join("\n")}</pre>
      )}
    </Sheet>
  );
}
