// «Настройки сервера → Перекодирование»: what re-encodes video (the GPU's
// backend or the CPU), the render node of VA-API, HDR tone mapping and HEVC
// output. A change is in effect at once for new playback; the line «Сейчас»
// says what the server picked and whether its test encode passed, and
// «Проверить заново» runs the test encodes again. Its own endpoint
// (/api/settings/transcoding), not the settings object: until changed here
// the server's VAULT_HWACCEL* apply.
import { useEffect, useState } from "react";
import {
  getTranscoding,
  probeTranscoding,
  saveTranscoding,
  errorText,
} from "../../api/client";
import type {
  TranscodingMode,
  TranscodingSettings as Values,
  TranscodingView,
} from "../../api/types";
import { useT } from "../../i18n";
import { Button } from "../../ui/Button";
import { GroupedSection, ListRow } from "../../ui/List";
import { Select } from "../../ui/Select";
import { Toggle } from "../../ui/Toggle";
import m from "./Manage.module.css";

/** «Сейчас»: whether video decodes and encodes on the GPU, and if not, why. */
export function statusLine(
  v: TranscodingView,
  t: ReturnType<typeof useT>,
): string {
  const s = v.status;
  if (s.mode === "none") {
    if (s.requested === "none") return t("settings.tc.status.cpuChosen");
    if (s.probes.some((p) => !p.ok)) return t("settings.tc.status.cpuFailed");
    if (v.devices.length === 0) return t("settings.tc.status.cpuNoDevice");
    return t("settings.tc.status.cpu");
  }
  const own = s.probes.find((p) => p.mode === s.mode);
  if (own && !own.ok) return t("settings.tc.status.gpuFailed", { name: s.name });
  const parts = [t("settings.tc.status.gpu", { name: s.name })];
  if (s.hevc) parts.push(t("settings.tc.status.hevc"));
  if (s.tonemapGpu) parts.push(t("settings.tc.status.tonemapGpu"));
  else if (s.tonemapCpu) parts.push(t("settings.tc.status.tonemapCpu"));
  return parts.join(" · ");
}

export function TranscodingSection() {
  const t = useT();
  const [view, setView] = useState<TranscodingView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ac = new AbortController();
    getTranscoding(ac.signal).then(
      setView,
      (e: unknown) =>
        !ac.signal.aborted &&
        setError(errorText(e, t("settings.tc.loadFailed"))),
    );
    return () => ac.abort();
  }, [t]);

  // a change probes the hardware on the server: seconds, so the row says so
  const run = async (call: () => Promise<TranscodingView>) => {
    setBusy(true);
    try {
      setView(await call());
      setError("");
    } catch (e) {
      setError(errorText(e, t("settings.tc.saveFailed")));
    } finally {
      setBusy(false);
    }
  };
  const change = (patch: Partial<Values>) => {
    if (!view) return;
    setView({ ...view, settings: { ...view.settings, ...patch } });
    void run(() => saveTranscoding({ ...view.settings, ...patch }));
  };

  if (!view) {
    return (
      <GroupedSection>
        <ListRow label={t("settings.group.transcoding")}>
          {error ? (
            <span className={m.problem} role="alert">
              {error}
            </span>
          ) : (
            <span className={m.spinnerRow} role="status">
              <span className={m.spinner} aria-hidden />
            </span>
          )}
        </ListRow>
      </GroupedSection>
    );
  }

  const v = view.settings;
  const modes: { value: TranscodingMode; label: string }[] = [
    { value: "auto", label: t("settings.tc.mode.auto") },
    { value: "qsv", label: "Intel Quick Sync" },
    { value: "vaapi", label: "VA-API" },
    { value: "nvenc", label: "NVIDIA NVENC" },
    { value: "videotoolbox", label: "VideoToolbox" },
    { value: "none", label: t("settings.tc.mode.none") },
  ];
  const devices = [
    {
      value: "",
      label: t("settings.tc.device.first", { device: view.devices[0] ?? "" }),
    },
    ...view.devices.map((d) => ({ value: d, label: d })),
  ];
  const showDevice =
    view.devices.length > 1 && (v.mode === "auto" || v.mode === "vaapi");
  return (
    <GroupedSection footer={t("settings.tc.footer")}>
      <ListRow
        stack
        label={t("settings.tc.mode")}
        hint={t("settings.tc.mode.hint")}
      >
        <Select
          label={t("settings.tc.mode")}
          placement="bottom-end"
          value={v.mode}
          options={modes}
          disabled={busy}
          onChange={(mode) => change({ mode })}
        />
      </ListRow>
      {showDevice && (
        <ListRow
          stack
          label={t("settings.tc.device")}
          hint={t("settings.tc.device.hint")}
        >
          <Select
            label={t("settings.tc.device")}
            placement="bottom-end"
            value={v.device}
            options={devices}
            disabled={busy}
            onChange={(device) => change({ device })}
          />
        </ListRow>
      )}
      <ListRow
        label={t("settings.tc.tonemap")}
        hint={t("settings.tc.tonemap.hint")}
      >
        <Toggle
          label={t("settings.tc.tonemap")}
          checked={v.tonemap}
          disabled={busy}
          onChange={(tonemap) => change({ tonemap })}
        />
      </ListRow>
      <ListRow label={t("settings.tc.hevc")} hint={t("settings.tc.hevc.hint")}>
        <Toggle
          label={t("settings.tc.hevc")}
          checked={v.hevc}
          disabled={busy}
          onChange={(hevc) => change({ hevc })}
        />
      </ListRow>
      <ListRow
        label={t("settings.tc.status")}
        status={
          error || (busy ? t("settings.tc.probing") : statusLine(view, t))
        }
      >
        <Button
          size="small"
          variant="secondary"
          icon="restart"
          loading={busy}
          disabled={busy}
          onClick={() => void run(probeTranscoding)}
        >
          {t("settings.tc.probe")}
        </Button>
      </ListRow>
      {view.custom && (
        <ListRow
          label={t("settings.tc.defaults")}
          hint={t("settings.tc.defaults.hint")}
        >
          <Button
            size="small"
            variant="secondary"
            disabled={busy}
            onClick={() => void run(() => saveTranscoding({ reset: true }))}
          >
            {t("settings.tc.defaults")}
          </Button>
        </ListRow>
      )}
    </GroupedSection>
  );
}
