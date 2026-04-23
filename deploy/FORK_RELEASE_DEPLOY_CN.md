# Fork 二进制发布与普通机器部署

本文说明如何使用自己的 fork 发布 Sub2API 二进制，并在不使用 Docker 的普通 Linux 机器上通过 systemd 部署。

适用场景：
- 代码来源是自己的 fork。
- 发布源是自己的 GitHub Releases。
- 服务器使用外部 PostgreSQL。
- 服务器本机安装 Redis。
- 不使用 Docker。

## 一、fork 发版前准备

### 1. 使用完整 Release 工作流

仓库已提供 `.github/workflows/release.yml`，会在推送 `v*` tag 时构建前端、嵌入前端资源、编译后端二进制，并通过 GoReleaser 发布 GitHub Releases。

注意：
- 不要开启 `simple_release`。
- 不要设置仓库变量 `SIMPLE_RELEASE=true`。
- `simple_release` 只发布 GHCR 镜像，不发布安装脚本需要的二进制压缩包。

### 2. 发布 tag

在你的 fork 本地仓库执行：

```bash
git tag v1.0.0-fork.1
git push origin v1.0.0-fork.1
```

GitHub Actions 完成后，你的 fork Releases 里应出现类似文件：

```text
sub2api_1.0.0-fork.1_linux_amd64.tar.gz
sub2api_1.0.0-fork.1_linux_arm64.tar.gz
checksums.txt
```

安装脚本依赖这些文件名。不要改 GoReleaser 的 `project_name: sub2api`、二进制名 `sub2api` 或归档命名规则，除非同步修改 `deploy/install.sh`。

## 二、服务器前置准备

以下示例以 Ubuntu/Debian 为例。

### 1. 安装 Redis

```bash
sudo apt update
sudo apt install -y redis-server
sudo systemctl enable redis-server
sudo systemctl start redis-server
redis-cli ping
```

建议给 Redis 加密码并限制本机访问：

```bash
sudo nano /etc/redis/redis.conf
```

推荐配置：

```conf
bind 127.0.0.1
requirepass 修改为你的强密码
maxmemory 256mb
maxmemory-policy allkeys-lru
```

重启验证：

```bash
sudo systemctl restart redis-server
redis-cli -a '你的Redis密码' ping
```

### 2. 准备外部 PostgreSQL

在外部 PostgreSQL 上创建数据库和用户。示例：

```sql
CREATE USER sub2api WITH PASSWORD '修改为你的强密码';
CREATE DATABASE sub2api OWNER sub2api;
GRANT ALL PRIVILEGES ON DATABASE sub2api TO sub2api;
```

如果使用云数据库，通常需要开启公网或内网访问白名单，并在 Sub2API 配置里使用 `sslmode: "require"`。

## 三、安装自己的 fork 版本

安装脚本支持通过 `--repo owner/repo` 指定 GitHub Releases 来源。

使用你的 fork 安装：

```bash
curl -sSL https://raw.githubusercontent.com/你的GitHub用户名/你的仓库名/main/deploy/install.sh \
  | sudo bash -s -- --repo 你的GitHub用户名/你的仓库名
```

安装指定版本：

```bash
curl -sSL https://raw.githubusercontent.com/你的GitHub用户名/你的仓库名/main/deploy/install.sh \
  | sudo bash -s -- --repo 你的GitHub用户名/你的仓库名 -v v1.0.0-fork.1
```

也可以用环境变量：

```bash
curl -sSL https://raw.githubusercontent.com/你的GitHub用户名/你的仓库名/main/deploy/install.sh \
  | sudo env SUB2API_GITHUB_REPO=你的GitHub用户名/你的仓库名 bash
```

安装后关键路径：

```text
/opt/sub2api/sub2api           # 运行中的二进制
/etc/sub2api/config.yaml       # 主配置文件
/etc/systemd/system/sub2api.service
```

## 四、启动前需要修改的配置

主配置文件是：

```bash
sudo nano /etc/sub2api/config.yaml
```

如果首次安装后还没有这个文件，可以先通过浏览器访问设置向导生成：

```text
http://服务器IP:8080
```

也可以手动创建 `/etc/sub2api/config.yaml`。2 核 2GB 机器建议使用较小连接池：

```yaml
server:
  host: "0.0.0.0"
  port: 8080

database:
  host: "你的外部PostgreSQL地址"
  port: 5432
  user: "sub2api"
  password: "你的PostgreSQL密码"
  dbname: "sub2api"
  sslmode: "require"
  max_open_conns: 20
  max_idle_conns: 5
  conn_max_lifetime_minutes: 30
  conn_max_idle_time_minutes: 5

redis:
  host: "127.0.0.1"
  port: 6379
  password: "你的Redis密码"
  db: 0
  pool_size: 50
  min_idle_conns: 5
  enable_tls: false

ops:
  enabled: false

jwt:
  secret: "使用 openssl rand -hex 32 生成"
  expire_hour: 24

totp:
  encryption_key: "使用 openssl rand -hex 32 生成"
```

生成密钥：

```bash
openssl rand -hex 32
openssl rand -hex 32
```

如需修改监听端口，也可以用 systemd override：

```bash
sudo systemctl edit sub2api
```

写入：

```ini
[Service]
Environment=SERVER_HOST=0.0.0.0
Environment=SERVER_PORT=8080
```

应用：

```bash
sudo systemctl daemon-reload
sudo systemctl restart sub2api
```

## 五、启动和检查

```bash
sudo systemctl enable sub2api
sudo systemctl start sub2api
sudo systemctl status sub2api
sudo journalctl -u sub2api -f
```

检查 Redis：

```bash
sudo systemctl status redis-server
redis-cli -a '你的Redis密码' ping
```

检查配置文件：

```bash
sudo cat /etc/sub2api/config.yaml
```

## 六、升级自己的 fork

发布新 tag 后，在服务器执行：

```bash
curl -sSL https://raw.githubusercontent.com/你的GitHub用户名/你的仓库名/main/deploy/install.sh \
  | sudo bash -s -- upgrade --repo 你的GitHub用户名/你的仓库名
```

升级到指定版本：

```bash
curl -sSL https://raw.githubusercontent.com/你的GitHub用户名/你的仓库名/main/deploy/install.sh \
  | sudo bash -s -- upgrade --repo 你的GitHub用户名/你的仓库名 -v v1.0.0-fork.2
```

回滚：

```bash
curl -sSL https://raw.githubusercontent.com/你的GitHub用户名/你的仓库名/main/deploy/install.sh \
  | sudo bash -s -- rollback --repo 你的GitHub用户名/你的仓库名 v1.0.0-fork.1
```

## 七、常见问题

### 安装脚本仍然下载官方版本

确认命令里带了：

```bash
--repo 你的GitHub用户名/你的仓库名
```

或环境变量：

```bash
SUB2API_GITHUB_REPO=你的GitHub用户名/你的仓库名
```

### Release 找不到安装包

检查你的 Release 是否存在：

```text
sub2api_<version>_linux_amd64.tar.gz
checksums.txt
```

如果没有这些文件，通常是误用了 `simple_release`，或启用了 `SIMPLE_RELEASE=true`。

### 私有 fork 能不能用

当前安装脚本默认走公开 GitHub API 和公开 Release 下载。私有 fork 不建议使用一键 curl 安装；更适合手动上传二进制到 `/opt/sub2api/sub2api`。
