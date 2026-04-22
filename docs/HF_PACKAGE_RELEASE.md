# Hugging Face 打包发布流程

这个项目发布到 Hugging Face Spaces 时使用 **package mode**，避免在
Space 构建机里重新执行前端构建和 Go 后端编译。

## 一次性配置

本地 `space` 远端应指向当前目标 Space：

```bash
git remote set-url space https://Vick888888@huggingface.co/spaces/Vick888888/gatewaySub
```

## 生成新的运行包

把代码推到 `myfork/main` 后，GitHub Actions 里的 `HF Package` 工作流会自动构建并上传固定名称的运行包：

```text
https://github.com/wanggang8/sub2api/releases/download/hf-latest/gatewayTestSub-hf-linux-amd64.tar.gz
```

也可以在 GitHub Actions 页面手动运行 `HF Package` 工作流，用来在没有新提交时刷新运行包。

## 发布到 Space

确认 GitHub Actions 已经成功上传运行包后，执行：

```bash
HF_PACKAGE_URL="https://github.com/wanggang8/sub2api/releases/download/hf-latest/gatewayTestSub-hf-linux-amd64.tar.gz" \
bash deploy/publish-hf-space.sh
```

在 package mode 下，推送到 Hugging Face Space 仓库的内容只有极简 `README.md` 和 `Dockerfile`。  
Dockerfile 会在 Space 构建时下载上面的运行包并启动，所以 Space 构建机不会再执行：

- `pnpm install`
- 前端构建
- `go build`

## 本地生成运行包

如需本地验证同样的包结构，可以执行：

```bash
bash deploy/build-hf-package.sh
```

生成的包会放在：

```text
release/hf/
```

## 注意事项

- Space 上的提交号和 `main` 不一样是正常的，因为 `publish-hf-space.sh` 会创建一个干净的一次性快照提交。
- 不要提交 `release/hf/*.tar.gz` 这类本地生成包。
- 如果运行包 URL 变了，通过 `HF_PACKAGE_URL` 传入新的地址即可。
- 当前 HF 运行时命令使用 `gatewayTestSub`，不要改回 `sub2api`，避免再次触发 Space 风控。
