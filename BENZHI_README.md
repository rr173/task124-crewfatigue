# task124-crewfatigue

航空机组飞行值勤与疲劳合规引擎（Aviation Crew Flight-Duty & Fatigue Compliance Engine）。

该服务解决支线航空公司机组排班合规性问题：注册机组（机长/副驾驶/巡航轮班）与机型（含休息设施等级），按行程（Trip）追加飞行航段（Flight Segment），相邻航段组成值勤期（Duty Period），值勤期之间登记休息期（Rest Period）。核心是一组围绕「FDP 上限表（报到本地时段 × 航段数）」「增编机组延长」「分段值勤抵扣」「滚动 28 天/168 小时/365 天累计上限」「最短休息/补偿性休息负债」「每周连续休息」「连续早班」「不可预见情况延长」的硬性疲劳规则，输入拟飞航段序列即输出「合法/不合法」+ 全部违规规则与数值明细。所有累计计数器在进程重启后从追加写事件日志（compliance_events）重放重建，且以重放结果为准（存储快照不一致则覆盖并记录 REPLAY_OVERRIDE）。主要输入为机组建档、行程航段、值勤期关闭、休息期登记与评估时刻；主要输出为合法性评估结果、滚动累计、休息负债与重放覆盖记录。全部时间以 UTC 存储，本地时区仅用于「报到时段」与「早班」判定；评估以调用方传入的 as-of 时刻驱动，不依赖墙钟。

## 本地命令

```bash
go build ./...      # 编译
go run . --addr :8080   # 启动 HTTP 服务（默认 :8080）
go run . --smoke-test   # 运行内置自检（内存 SQLite，跑完整业务闭环后退出）
go test ./...       # 运行测试
```

浏览器前端：<http://localhost:8080/> （机组建档/机型注册/行程航段/值勤期关闭/休息登记/合规评估/累计与负债查询）。前端为原生 HTML/CSS/JS，嵌入 Go 二进制（`internal/webfs/web/`），无独立构建步骤。

## Docker 构建

`build_benzhi_docker.sh <镜像名> <平台>` 接受两个参数：镜像名（默认 `my-project`）与目标平台（默认 `linux/amd64`）。

amd64 与 arm64 双架构构建命令：

```bash
bash ./build_benzhi_docker.sh go-task-benzhi:amd64 linux/amd64
docker run --rm go-task-benzhi:amd64 bash -c 'go run . --smoke-test'

bash ./build_benzhi_docker.sh go-task-benzhi:arm64 linux/arm64
docker run --rm go-task-benzhi:arm64 bash -c 'go run . --smoke-test'
```

进入容器交互 shell：

```bash
docker run -it go-task-benzhi:amd64
```

容器内 Go 工具链版本：`go1.26.3`（`go version` 可验证）。前端为原生 HTML/CSS/JS，嵌入 Go 二进制，无独立构建步骤；镜像内 `go build ./...` 同时验证后端与前端嵌入。

## smoke-test

`go run . --smoke-test` 在内存 SQLite 上跑完整业务闭环并退出（退出码 0 表示通过）：

- 注册 3 种机型 + 1 名机长；
- R1：2 段昼间行程 FDP 225min < 14h(840) → LEGAL；7 段昼间 FDP 835min > 11h(660) → ILLEGAL(R1)；
- R2：增编 B789（CLASS_1 +4h）→ 上限 660+240=900 → LEGAL；B738（NONE）即使增编也不延长；
- R3：240min 分段休息抵扣 → 上限 780→1020；<3h（179min）不抵扣；
- R4/R5：关闭真实值勤期后，滚动 28 天/168 小时累计反映真实 block/FDP；
- R7：首次 9h 减少休息允许（产生负债），第二次减少休息在负债未清时触发 R7；<9h 下限恒违规；
- R9：连续 6 次早班（report 本地 <07:00）无 36h 重置 → 触发 R9；
- R10：2 次不可预见延长后 `rest-debt` 显示 owes_augmented_rest=true、本年已用 2 次；
- 恢复：`/admin/replay` 对无事件机组返回 0 覆盖；对有事件机组首次 3 覆盖、二次收敛 0；
- 前端：`GET /` 返回页面且含「机组飞行值勤与疲劳合规引擎」标题，`GET /api/crew` 返回非空数组。

任一断言失败即以非零码退出。
