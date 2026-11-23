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
| 权限验证 | 以 multica 账号实际连接成功；public schema owner=multica、CREATE=true、业务表数=0 |
| 新入口 | `https://multica.vita.cn`，公网 `alb`、`letsencrypt-dns01` 独立证书 |
| 当前阶段 | 镜像已转存、建库已完成；应用尚未开放，数据库恢复和最终切换尚未执行 |

源端仍在处理任务。迁移不会重建 `multica-postgres-1`，也不修改或提交源机的 Compose 卷路径。

### 镜像

首次使用源端正在运行的镜像原样转存，未重新构建或升级。以下 image ID 经源机
`docker image inspect` 核实；ACR digest 经云 API 及源机 RepoDigests 双向核对。
两个私有镜像仓库均已开启 tag immutability。

| 组件 | tag | 源 image ID | ACR manifest digest |
| --- | --- | --- | --- |
| 后端 | `v0.8.2-smtp-feishu-c5574b8cf22a` | `sha256:c5574b8cf22a4a489e7a52bf931945f1e7f0d19840c51cedf2fc4aadf0c0df20` | `sha256:a5dbde4dd4d6f279955baeda9d8ce077764ae6dd84f075bb957420fb7a41a6c1` |
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
- 源端 pgcrypto=1.3，RDS 可用版本=1.4；恢复演练须验证扩展兼容性。

## 第一阶段：资源准备

当前 values 将前后端 replicas 设为 **0**，resources 白名单没有包含公网 Ingress。
首次同步只准备 PVC、证书和密钥，不运行业务进程，不争抢源端 Runtime/飞书连接。
Ingress 模板保存在 `resources/ingress.yaml.pending`，不会被 Kustomize 或 Argo CD 渲染。

1. 先合入本仓库 PR 到 `chore/selfhost-local-config`，再合 vt-ops 的 Application 注册。
2. 手动同步 Multica Application，等待 Certificate Ready，确认 SealedSecret Synced。
3. 确认 PVC 20Gi、StorageClass 和挂载权限。它采用延迟绑定，等待挂载 Pod 后才会 Bound。
4. 源库在线预备备份用于恢复演练；与附件并非同一停写时间点，不能当作最终切换备份。
   源机备份目录为 `/mnt/storage/data/xucheng.zhang/data/multica/migration-20261123`，权限 0700。
5. 目标库恢复、schema/扩展等 RDS 写入默认走 DMS；若用户明确豁免，才走受控数据库客户端。

共享 RDS 本次 Terraform apply 使用现有 default 白名单作为显式输入，plan 为
`4 to add, 0 to change, 0 to destroy`。保留了现网额外的 `8.145.62.60/32`，
未将它混入此 PR；仓库对此 IP 的既有漂移仍需单独收敛，之后全量 apply 前必须检查。

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

正式切换前确认停写窗口：

1. 排空运行中任务，阻断旧 HTTP/WS 的写入口，停止旧后端及其他写入进程，源 PG 保持运行。
   核对旧 pg_cron 仍可能写汇总数据，最终备份期间暂停该 job 或明确冻结对应写入。
2. 再导出最终数据库并同步附件，记录同一停写窗口的备份标识。源库保留作回退。
3. 将最终数据恢复到确认空白的生产目标，校验数据及附件后，提交 replicas=1 的变更。
4. 先通过集群内/端口转发核对 API 与前端；证书 Ready 后将 ingress.yaml.pending
   重命名为 ingress.yaml 并加入 resources，再实测公网 HTTPS、登录、旧数据、上传下载、WS
   和真实 Runtime 任务。
5. 迁移 Agent/Daemon 配置与外部回调后恢复任务提交。旧域名不会自动重定向到新域名。
   检查仍连旧入口的客户端，按实际调用点更新，不依据 DNS 切换推断它们已经迁移。

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
