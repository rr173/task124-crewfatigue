// Native frontend (no build step). Talks to the Go API under /api.
const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));
const api = (path, opts) => fetch(path, {
  headers: { "Content-Type": "application/json" }, ...opts,
}).then(async (r) => {
  const txt = await r.text();
  let body = null;
  if (txt) { try { body = JSON.parse(txt); } catch { body = txt; } }
  if (!r.ok) { const msg = (body && body.error) || ("HTTP " + r.status); throw new Error(msg); }
  return body;
});
const formJSON = (form) => {
  const fd = new FormData(form);
  const o = {};
  for (const [k, v] of fd.entries()) {
    if (v === "") continue;
    if (form.querySelector(`[name="${k}"]`)) {
      const el = form.querySelector(`[name="${k}"]`);
      if (el.type === "checkbox") o[k] = el.checked;
      else if (el.type === "number") o[k] = Number(v);
      else o[k] = v;
    }
  }
  return o;
};
const setStatus = (m) => { $("#status").textContent = m; };
const fmtTime = (s) => s ? new Date(s).toISOString().replace("T"," ").replace("Z","Z") : "";

// --- tabs ---
$$(".tab").forEach((b) => b.addEventListener("click", () => {
  $$(".tab").forEach((x) => x.classList.remove("active"));
  $$(".panel").forEach((x) => x.classList.remove("active"));
  b.classList.add("active");
  $("#tab-" + b.dataset.tab).classList.add("active");
}));

// --- crew ---
$("#form-crew").addEventListener("submit", async (e) => {
  e.preventDefault();
  try { await api("/api/crew", { method: "POST", body: JSON.stringify(formJSON(e.target)) }); setStatus("机组已注册"); loadCrew(); }
  catch (err) { setStatus("错误: " + err.message); }
});
$("#form-aircraft").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  o.has_rest_facility = String(o.has_rest_facility) === "true";
  try { await api("/api/aircraft-types", { method: "POST", body: JSON.stringify(o) }); setStatus("机型已注册"); loadAircraft(); }
  catch (err) { setStatus("错误: " + err.message); }
});
async function loadCrew() {
  const list = await api("/api/crew");
  $("#crew-table tbody").innerHTML = (list || []).map((c) =>
    `<tr><td>${c.id}</td><td>${c.name}</td><td>${c.role}</td><td>${c.home_base}</td><td>${c.home_tz}</td><td>${c.active ? "在职" : "停飞"}</td></tr>`
  ).join("");
}
async function loadAircraft() {
  const list = await api("/api/aircraft-types");
  $("#aircraft-table tbody").innerHTML = (list || []).map((a) =>
    `<tr><td>${a.code}</td><td>${a.has_rest_facility ? "有" : "无"}</td><td>${a.rest_facility_class}</td></tr>`
  ).join("");
}

// --- trip ---
$("#form-trip").addEventListener("submit", async (e) => {
  e.preventDefault();
  try { const t = await api("/api/trips", { method: "POST", body: JSON.stringify(formJSON(e.target)) }); setStatus("行程 " + t.id + " 已创建"); }
  catch (err) { setStatus("错误: " + err.message); }
});
$("#form-segment").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  try {
    const s = await api(`/api/trips/${o.trip_id}/segments`, { method: "POST", body: JSON.stringify(o) });
    setStatus("航段 " + s.id + " 已追加");
    listSegs(o.trip_id);
  } catch (err) { setStatus("错误: " + err.message); }
});
$("#form-list-seg").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  await listSegs(o.trip_id);
});
async function listSegs(tripID) {
  const list = await api(`/api/trips/${tripID}/segments`);
  $("#segment-table tbody").innerHTML = (list || []).map((s) =>
    `<tr><td>${s.id}</td><td>${s.aircraft_type}</td><td>${s.dep_airport}→${s.arr_airport}</td><td>${fmtTime(s.scheduled_dep)}</td><td>${fmtTime(s.scheduled_arr)}</td><td>${s.block_time_min}</td></tr>`
  ).join("");
}

// --- duty / rest ---
$("#form-duty").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  try { const d = await api("/api/duty-periods", { method: "POST", body: JSON.stringify(o) });
    setStatus("值勤期 " + d.id + " 已创建"); loadDuties(); }
  catch (err) { setStatus("错误: " + err.message); }
});
$("#form-close-duty").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  try { const d = await api(`/api/duty-periods/${o.duty_id}/close`, { method: "POST", body: JSON.stringify({ unforeseen_min: o.unforeseen_min || 0 }) });
    setStatus("值勤期 " + d.id + " 已关闭 (FDP " + Math.round((new Date(d.release_time)-new Date(d.report_time))/60000) + "min)"); loadDuties(); }
  catch (err) { setStatus("错误: " + err.message); }
});
$("#form-list-duty").addEventListener("submit", (e) => { e.preventDefault(); loadDuties(); });
async function loadDuties() {
  const list = await api("/api/duty-periods");
  $("#duty-table tbody").innerHTML = (list || []).map((d) => {
    const fdp = Math.round((new Date(d.release_time) - new Date(d.report_time)) / 60000);
    return `<tr><td>${d.id}</td><td>${d.crew_id}</td><td>${d.trip_id}</td><td>${fmtTime(d.report_time)}</td><td>${fmtTime(d.release_time)}</td><td>${fdp}</td><td>${d.is_augmented ? "是" : "否"}</td><td>${d.status}</td></tr>`;
  }).join("");
}
$("#form-rest").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  try { const r = await api("/api/rest-periods", { method: "POST", body: JSON.stringify(o) }); setStatus("休息期 " + r.id + " 已登记"); loadRest(); }
  catch (err) { setStatus("错误: " + err.message); }
});
async function loadRest() {
  const list = await api("/api/rest-periods");
  $("#rest-table tbody").innerHTML = (list || []).map((r) =>
    `<tr><td>${r.id}</td><td>${r.crew_id}</td><td>${fmtTime(r.start)}</td><td>${fmtTime(r.end)}</td><td>${r.rest_type}</td><td>${r.duration_min}</td><td>${r.covers_weekly ? "是" : "否"}</td></tr>`
  ).join("");
}

// --- eval ---
$("#form-eval").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  let segs = [];
  try { segs = JSON.parse(document.querySelector("[name=segments_json]").value || "[]"); }
  catch (err) { setStatus("航段 JSON 解析失败: " + err.message); return; }
  const body = { aircraft_type: o.aircraft_type, is_augmented: !!o.is_augmented, split_break_min: o.split_break_min || 0, segments: segs };
  if (o.as_of) body.as_of = o.as_of;
  try {
    const ev = await api(`/api/crew/${o.crew_id}/evaluate-trip`, { method: "POST", body: JSON.stringify(body) });
    renderEval(ev);
  } catch (err) { $("#eval-result").textContent = "评估失败: " + err.message; setStatus("错误: " + err.message); }
});
function renderEval(ev) {
  const cls = ev.verdict === "LEGAL" ? "verdict-legal" : "verdict-illegal";
  const v = (ev.violations || []).map((x) => `  • [${x.rule}] ${x.message} (实际 ${x.actual} / 限 ${x.limit})`).join("\n");
  const m = ev.metrics || {};
  $("#eval-result").innerHTML =
    `<div class="${cls}">${ev.verdict}</div>` +
    `FDP: ${m.fdp_min}/${m.fdp_limit_min} min · 28天: ${m.used_28d_min}/${m.limit_28d_min} · 168h: ${m.used_168h_min}/${m.limit_168h_min} · 365天: ${m.used_365d_min}/${m.limit_365d_min}\n` +
    `早班连续: ${m.early_start_streak}/${m.early_start_limit} · 不可预见: ${m.unforeseen_used_year}/${m.unforeseen_limit_year} 次/年 · 距上次休息: ${m.rest_since_last_min}/${m.min_rest_min} min\n` +
    (v ? `违规:\n${v}` : "(无违规)");
}
$("#form-cum").addEventListener("submit", async (e) => {
  e.preventDefault();
  const o = formJSON(e.target);
  try {
    const snap = await api(`/api/crew/${o.crew_id}/cumulative`);
    const debt = await api(`/api/crew/${o.crew_id}/rest-debt`);
    $("#cum-result").innerHTML =
      `累计 (截至 ${fmtTime(snap.as_of)}): 28天=${snap.used_28d_min}min · 168h=${snap.used_168h_min}min · 365天=${snap.used_365d_min}min\n` +
      `休息负债: 欠补偿=${debt.compensatory_owed_min}min · 欠每周休息=${debt.owes_weekly_rest} · 欠增编休息=${debt.owes_augmented_rest} · 早班连续=${debt.early_start_streak} · 本年不可预见=${debt.unforeseen_used_year}`;
    setStatus("已查询累计与负债");
  } catch (err) { $("#cum-result").textContent = "查询失败: " + err.message; setStatus("错误: " + err.message); }
});

// --- init ---
loadCrew(); loadAircraft(); loadDuties(); loadRest();
