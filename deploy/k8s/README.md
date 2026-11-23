# Multica → ACK / Argo CD

本目录用于迁移 `bj-dev02.office` 的二开自托管服务。Argo CD 的两个 source 均跟踪
`chore/selfhost-local-config`：`deploy/helm/multica` 使用本目录的 values，
`deploy/k8s/resources` 管理 Namespace、SealedSecret 和 Certificate。
应用注册及共享数据层声明位于 `VitaDynamics/vt-ops`。

## 现网基线与已完成准备（2026-11-23）

| 项目 | 已核实的实际值 |
| --- | --- |
| 源端 | JumpServer → `bj-dev02.office` / `10.100.10.4`，root 登录后的 hostname 为 `vbot-dev` |
| 工作区 | `/home/xucheng.zhang/code/multica`，提交 `231db61eed2df93a4d187703d16ea434ca522407` |
| 分支 | `chore/selfhost-local-config`；Compose 有两处有意保留的宿主机卷路径改动 |
| 旧入口 | `https://multica.vbot.cn:8443`；前端宿主机端口 13000，后端 18080 |
| 数据库 | `multica-postgres-1`，PG17.9，约 5297 MB，660 条 migration，最后为 `636_site_counter` |
| 源端扩展 | `pgcrypto 1.3`、`pg_trgm 1.6`、`pg_cron 1.6`，无 vector |
| 附件 | `/mnt/storage/data/xucheng.zhang/data/multica/uploads` → `/app/data/uploads`，约 1.5 GB |
| 目标库 | 共享 `pgm-2ze9l2wdgc81f3xm`，PG18；独立 `multica` 库和 Normal 账号已通过 Terraform 创建 |
| 恢复前权限验证 | 以 multica 账号实际连接成功；public schema owner=multica、CREATE=true、业务表数=0 |
| 新入口 | `https://multica.vita.cn`，公网 `alb`、`letsencrypt-dns01` 独立证书 |
| 当前阶段 | 初始数据库/附件恢复及全量校验完成，migrator no-op；旧业务运行中，最终停写及切流尚未执行 |

源端仍在处理任务。本次仅进行在线预备恢复与校验，最终停写及切流另行确认。迁移不会重建 `multica-postgres-1`，也不修改或提交源机的 Compose 卷路径。

### 镜像

首次使用源端正在运行的镜像原样转存，未重新构建或升级。以下 image ID 经源机
`docker image inspect` 核实；ACR digest 经云 API 及源机 RepoDigests 双向核对。
两个私有镜像仓库均已开启 tag immutability。

| 组件 | tag | 源 image ID | ACR manifest digest |
| --- | --- | --- | --- |
| 后端 | `v0.8.2-smtp-feishu-c5574b8cf22a-rds-private` | `sha256:c5574b8cf22a4a489e7a52bf931945f1e7f0d19840c51cedf2fc4aadf0c0df20` | `sha256:a5dbde4dd4d6f279955baeda9d8ce077764ae6dd84f075bb957420fb7a41a6c1` |
| 前端 | `v0.8.2-smtp-feishu-35280f1467bb` | `sha256:35280f1467bb015b86f4a7ff0bf62ee5d40a1f1404ae0a7def20aa2a22996591` | `sha256:14e3422a6bed2e51c236c56ab89f2f3c34c9bb0e7bbc78188cc94c50d94de821` |

仓库前缀为 `vbot-acr-registry-vpc.cn-beijing.cr.aliyuncs.com/vbot-platform/`，分别名为
`multica-backend` 和 `multica-web`。ACK ACR helper 负责拉取凭据，vt-ops 同时注册
multica namespace 的 default ServiceAccount。

### 配置与数据差异

- 保留源端 `APP_ENV=development`、注册策略、SMTP TLS、飞书验证码镜像、JWT 及集成密钥。
  更换 APP_ENV 属于独立行为变更，首迁不同时进行。
- 新的前端 Origin、CORS、App URL、Public URL 改为 `https://multica.vita.cn`。
  Google OAuth 当前未启用，原有 redirect 配置保留，启用前另行调整回调。
- 非敏感 chart 配置位于 values，其余实际运行环境通过 strict-scope SealedSecret 注入，
  明文和数据库密码不入仓。加密目标固定为 `multica/multica-secrets`。
- 前端使用 `REMOTE_API_URL=http://multica-backend:8080`。保留 chart 的 `backend`
  Service 别名以兼容当前镜像；WebSocket 在 ALB 直接转发到后端。
- 附件使用 20Gi RWO PVC、WaitForFirstConsumer StorageClass；后端 Recreate、单副本。
- 目标实例未预加载 pg_cron。二开服务已内置同一 `rollup_task_usage_hourly` 的 5 分钟调度，
  源端 `sys_cron_executions` 已实证它正在执行。因此备份排除 pg_cron 及 cron schema，
  不在共享实例启用扩展、不修改共享参数、不增加重复 CronJob。
- 源端 pgcrypto=1.3，目标实测恢复为 pgcrypto=1.4、pg_trgm=1.6；完整恢复和原镜像 migrator 均成功。
- 源库时区为 `Etc/UTC`、DateStyle 为 `ISO, MDY`；目标库原默认值为 Asia/Shanghai、ISO YMD。
  已仅对新建 multica 库设置与源端一致的数据库级默认值，未修改共享实例参数。
- 源端 DATABASE_URL 使用 sslmode=disable；目标实例实测未启用 SSL。连接串沿用
  sslmode=disable 并走 ACK→RDS VPC 内网；首版 require 已纠正。后端镜像增加配置版本 tag，
  镜像内容及 manifest digest 保持不变。

## 第一阶段：资源准备

当前 values 将前后端 replicas 设为 **0**，resources 白名单没有包含公网 Ingress。
首次同步只准备 PVC、证书和密钥，不运行业务进程，不争抢源端 Runtime/飞书连接。
Ingress 模板保存在 `resources/ingress.yaml.pending`，不会被 Kustomize 或 Argo CD 渲染。

1. 先合入本仓库 PR 到 `chore/selfhost-local-config`，再合 vt-ops 的 Application 注册。
2. 手动同步 Multica Application，等待 Certificate Ready，确认 SealedSecret Synced。
3. PVC 已由临时迁移 Pod 触发延迟绑定，20Gi 云盘 `d-2ze8csyjk2i64dpospvr` 已 Bound。
   Certificate Ready、应用 Synced/Healthy，前后端仍为 0 副本。
4. 源库在线预备备份用于恢复演练；与附件并非同一停写时间点，不能当作最终切换备份。
   源机备份目录为 `/mnt/storage/data/xucheng.zhang/data/multica/migration-20261123`，权限 0700。
5. 本次用户已明确豁免 DMS，允许使用受控 pg_restore 仅向新建 multica 空库恢复预备备份。
   恢复前再次核实库名、账号和空库状态。

在线预备备份已完成，pg_restore TOC 检查通过（962 行，无 pg_cron extension 条目）：

| 文件 | 字节数 | SHA-256 |
| --- | --- | --- |
| `database.dump` | 1766888707 | `164dadd1a6bcdc4f21468b2ba13031f53a372dae5b0740fcc04004085a127e92` |
| `uploads.tar.gz` | 1287164211 | `ca45597b4e9d74f19c663f6b8fe2e2512609fb8628f2d76377512a28f84370f1` |


共享 RDS 本次 Terraform apply 使用现有 default 白名单作为显式输入，plan 为
`4 to add, 0 to change, 0 to destroy`。保留了现网额外的 `8.145.62.60/32`，
未将它混入此 PR；仓库对此 IP 的既有漂移仍需单独收敛，之后全量 apply 前必须检查。

### 初始数据库恢复结果

恢复开始前已重新确认目标 host、库名、账号和空库状态，归档在集群内重新计算 SHA-256 后
以 `--no-owner --no-acl --no-tablespaces --single-transaction --exit-on-error` 执行。
**2026-11-23 20:35–21:06 CST 成功提交，耗时 1899.72 秒（31 分 40 秒），exit code 0**。
141 张 public 表、660 条 migration，最后版本 `636_site_counter`；目标扩展为
pgcrypto 1.4、pg_trgm 1.6、plpgsql 1.0，没有引入 pg_cron。

恢复后逐表执行相同列顺序的 COPY 输出校验，在 UTC/ISO MDY/bytea hex 等一致的输出参数下，
**141 张表、9,988,776 行的精确行数及每行 SHA-256 的顺序无关 XOR/模加和全部一致**，
耗时 471.40 秒（7 分 51 秒），mismatch=0、invalid index=0。
3 条 NOT VALID CHECK 约束的表、名称、类型、定义与源库完全一致，保持源状态：
`agent_task_queue_active_requires_runtime`、`issue_wakeup_actor_filter`、
`runtime_profile_protocol_family_check`。不擅自 VALIDATE 或修改业务约束。

使用当前后端镜像创建独立 `/app/migrate up` Job，660 个版本均显示 already applied，
Job Complete；未启动 server、Runtime 或飞书连接。恢复耗时必须计入正式停写窗口，
不能只依据传输补丁大小估计停机时间。141 张表 ANALYZE 已完成，耗时 42.58 秒。

源端最终复查前后端健康接口均为 HTTP 200，仍有 1 个运行中任务，未停止业务。
本次临时传输监听已关闭，源机临时证书/私钥/token 目录及 Kubernetes 临时传输 Secret 已删除；
本机同用途临时凭据已清理。基线备份、校验报告及迁移工具 Pod 暂保留供最终窗口使用。

### 初始附件同步结果

附件归档传到集群后 SHA-256 与源端一致，安全解包至 20Gi PVC 并逐文件独立读回校验：
**20,125 个文件、1,536,210,861 字节，全部摘要一致**。解包及读回耗时 12.42 秒。
归档中的 10,080 条 attachment 引用均为相对 URL，对应文件全部存在且大小与数据库记录一致。
这是在线预备副本；最终停写窗口仍须同步后续新增、变更与删除，不能跳过附件终态校验。

## 备份、恢复演练与切换

源库使用容器内匹配版本的客户端：

```bash
# pg_cron 已由应用调度替代，避免恢复时要求重启共享 RDS。
docker exec multica-postgres-1 pg_dump -U multica -d multica -Fc \
  --exclude-extension=pg_cron --exclude-schema=cron > database.dump
# 通过 stdin 检验归档时必须使用 -i。
docker exec -i multica-postgres-1 pg_restore --list < database.dump > database.list
```

备份文件权限 0600，记录字节数、SHA-256、导出时间及 schema_migrations 清单。
传输后复算 SHA-256；附件传输后核对文件数量、总字节和逐文件摘要。
数据库恢复仅在确认目标为空、连接库为 multica 且账号为 multica 后进行。
恢复不设置 Argo CD hook，避免每次同步重复写入：

```bash
# 使用受保护的连接文件/PGPASSFILE，密码不得放入命令参数或 Git。
pg_restore --no-owner --no-acl --single-transaction --exit-on-error \
  --dbname=multica database.dump
```

以实际导出的 TOC 检查 public schema 和扩展所有权：若目标已自带 public schema，
恢复清单只排除这一条 CREATE SCHEMA，保留表、函数及其他对象；不得用 --clean 清理已有库。
源 pgcrypto 扩展版本和 RDS 默认版本不同，须先在演练中验证，不根据版本号直接宣称兼容。

恢复演练至少验证：660 条 migration、关键表数量/最新业务记录、序列值、扩展、索引状态、
附件抽样和 migrate no-op。演练期间只启动 migrator，不启动 server，避免后台任务/飞书连接
对生产外部系统产生副作用。需要完整服务验收时，先准备隔离副本和出站限制。

正式切换建议预留 **60–90 分钟维护窗口**：本次完整恢复 31 分 40 秒、全量行校验
7 分 51 秒、统计信息更新 43 秒；另需最终备份/SQL 重建、附件同步、切流及验收余量。
附件可重新传输停写后的完整归档（本次传输 24 分 5 秒），与数据库恢复并行，确保新增、
变更和删除均以最终快照为准，不依赖尚未验证的增量文件逻辑。实际时长以最终数据量为准。

正式切换前确认停写窗口：

1. 排空运行中任务，阻断旧 HTTP/WS 的写入口，停止旧后端及其他写入进程，源 PG 保持运行。
   核对旧 pg_cron 仍可能写汇总数据，最终备份期间暂停该 job 或明确冻结对应写入。
2. 再导出最终数据库并同步附件，记录同一停写窗口的备份标识。源库保留作回退。
   目标 multica 库现已有演练数据；必须单独确认仅重置该新库的范围及 DMS 豁免后再操作，
   不清理共享实例其他数据库、账号或参数。
3. 将最终数据恢复到确认空白的生产目标，校验数据及附件；删除 PVC 内临时 SQL 工作目录，
   导出需保留的报告并删除临时迁移 Pod，释放 RWO 云盘挂载，再提交 replicas=1 的变更。
4. 先通过集群内/端口转发核对 API 与前端；证书 Ready 后将 ingress.yaml.pending
   重命名为 ingress.yaml 并加入 resources，再实测公网 HTTPS、登录、旧数据、上传下载、WS
   和真实 Runtime 任务。
5. 迁移 Agent/Daemon 配置与外部回调后恢复任务提交。旧域名不会自动重定向到新域名。
   检查仍连旧入口的客户端，按实际调用点更新，不依据 DNS 切换推断它们已经迁移。

### 最终窗口的传输优化实测

办公网到 ACK 的本次有效传输约 0.8–1 MiB/s，初始 1.77 GB 数据库归档传输耗时
2083.79 秒。为避免停写窗口再次等待完整 WAN 传输，已完成以下跨客户端、跨架构实证：

1. 源数据库两次在线完整 dump，均为原生完整归档。
2. 使用固定且受保护的 `--restrict-key`，以同一 TOC 规则执行
   `pg_restore --no-owner --no-acl --no-tablespaces --file`，生成规范化 SQL。
   Mac pg_restore 18.4 与集群 17.12 对同一归档生成的 SQL 字节完全一致。
3. 官方 Xdelta 3.2.2 将两份 SQL 的差异编码为 2,757,460 字节补丁；Mac arm64 和
   集群 Linux amd64 均成功重建完整 SQL，并通过 SHA-256 校验。

| 对象 | 字节数 | SHA-256 |
| --- | --- | --- |
| 基线 SQL | 5962907597 | `8660101919f1505fe098dda3b8175ab40ce3bc35a96847e9a6762bfe148ad956` |
| 重建 SQL | 5976548497 | `d209bf2789f4f94bf8d3cac70d341934320bb67d7e3c86e208ada4449be71c91` |

集群基线生成及校验 88.26 秒，重建及校验 174.50 秒。临时 SQL 工作目录已删除；
工具与基线归档受保护保留。该方案仅优化传输，最终仍恢复完整原生 SQL，恢复时间不能省略。
2.76 MB 是这两次实测的差异大小，不能保证最终窗口仍为该大小。
最终执行须先确认停写、重置演练库和切流授权；目标库已有数据时不能直接重复恢复。

### 已核实的旧入口与 Runtime

- 旧域名 A 记录为办公网公网 `43.226.238.206`；源机 Nginx Proxy Manager
  `npm-app-1` 同时映射宿主机 443/8443，配置 `/root/npm/data/nginx/proxy_host/1.conf`
  将 `multica.vbot.cn` 转发到 `http://10.100.10.4:13000`，并透传 WebSocket Upgrade。
- 两个运行中的 Runtime 直接使用 `http://10.100.10.4:18080`，需在最终窗口处理：
  `ubuntu-dev-cpz` 的 `/home/multica/.multica/config.json`；
  `vita-dev` 的 `/home/devuser/.multica/config.json`。仅核对 server_url，未输出或修改 token。
  容器的进程 HOME 与宿主机同 UID 的 passwd home 不同，配置以进程命名空间和 HOME 为准。
- 另有 Runtime 指向 `https://api.multica.ai`，不属于本次自建服务迁移，保持原样。
- 2026-11-23 20:57 CST 源库实测：4 个 running、1 个 queued 任务，Runtime 33 online、5 offline。
  两个本机容器不是完整客户端清单；最终窗口需排空任务并核对其他主机上的 Runtime。
- 保留旧入口兼容与直接迁移 Runtime 是两项独立步骤；仅发布新域名不能消除旧地址调用。
  GitHub App webhook 已通过只读 `GET /app/hook/config` 实测为
  `https://multica.vbot.cn:8443/api/webhooks/github`（json，SSL 验证开启）；
  切换时需同步更新为新域名对应路径，或提供旧入口兼容。未修改外部设置。Google OAuth client 当前为空。

## 回退与完成条件

新环境尚未开放写入时，可停新后端并恢复旧服务及客户端地址。新环境已产生写入后，
先冻结并备份目标数据库和附件，处理差异后再决定回退，不能直接指回旧快照。

完成以实际验证为准：Application Synced/Healthy、业务 Pod Ready、恢复与附件摘要核对、
公网 TLS、登录权限、任务读写、文件上传下载、WebSocket 和至少一条真实 Runtime 任务。
本目录的渲染检查通过不代表迁移完成。

```bash
helm template multica deploy/helm/multica -n multica -f deploy/k8s/values-vt-prod.yaml
kubectl kustomize deploy/k8s/resources
```
