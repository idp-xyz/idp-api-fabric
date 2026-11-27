# fabric-import：从契约仓库导入修订

`fabric-import` 把一个文件夹里的契约文件作为一个 API 资产的新修订导入 Fabric，调用的是管理 API 的 `importRevision`。它主要给契约仓库的 CI 用：合并以后，CI 把合并进来的文件连同 Git 来源一起导入。

```bash
export IDP_FABRIC_API_URL=https://fabric.example
export IDP_FABRIC_TOKEN=…   # 资产所在 Workspace 的作者的访问令牌
fabric-import -product cargopai -asset shipments-api -dir contracts/shipments -git
```

输出一行：修订序号、编号、状态，这次是新记录还是已有记录，入口文件、文件数和源文件摘要。

```text
revision #3 2e0dd5ba-… DRAFT, recorded now: entrypoint contracts/shipments/openapi.yaml, 2 files, sha256:89f3…
```

同一组文件再次导入，得到的是第一次记录的那个修订（`recorded before`），所以 CI 重跑不会产生重复修订。

## 参数

| 参数 | 说明 |
|---|---|
| `-api` | Fabric 管理 API 的源站，默认取 `IDP_FABRIC_API_URL`。只接受 https；http 只接受本机地址 |
| `-product` | 产品的键或编号 |
| `-asset` | API 资产的键或编号。用键时按键精确匹配当前令牌看得到的资产 |
| `-dir` | 存放契约文件的文件夹，默认当前目录 |
| `-entry` | 入口文件，相对于 `-dir`。默认取文件夹顶层的 `openapi.yaml`、`.yml` 或 `.json` |
| `-git` | 声明这些文件来自所在的 Git 仓库：remote 的地址、检出的提交，以及分支或标签 |
| `-remote` | 与 `-git` 一起用，指定用哪个 remote 的地址，默认 `origin` |

访问令牌只从环境变量 `IDP_FABRIC_TOKEN` 读取，不提供命令行参数，免得令牌出现在进程列表和 shell 历史里。令牌对应的账号须是资产所在 Workspace 的 `internal_author`。在 CI 中，令牌由企业身份系统为 CI 的服务账号签发。

## 发送哪些文件

工具发送文件夹下所有 YAML 和 JSON 文件，隐藏文件夹（如 `.git`）不进入。入口文件和被它引用的文件由服务端记录。发送前检查以下各项，不满足时直接报错，不发请求：
- 文件数不超过 16 个，合计不超过 40960 字节；
- 路径只含字母、数字、`.`、`_` 和 `-`；
- 内容是 UTF-8。

一个文件夹对应一个资产，有多个资产时分别调用。

## Git 来源

带 `-git` 时，来源按下面的方式确定：

- **仓库**：取 remote 的地址，写成 https 形式，去掉其中的凭据和末尾的 `.git`，主机名改为小写。SSH remote（`git@github.com:org/repo.git`）改写成同一主机、同一路径的 https 地址，GitHub 和 GitLab 就是这样提供的。
- **提交**：当前检出的提交。
- **分支或标签**：在 GitHub Actions 里取 `GITHUB_REF`；否则取当前分支；分离检出时，取指向这个提交的标签；都没有时不填。
- **路径**：文件路径和入口文件都是仓库里的路径。

要发送的每个文件都必须和这个提交里的内容逐字节一致。有改动未提交或者文件未纳入版本控制时，工具拒绝导入，因为声明的来源要如实。Fabric 只记录这一声明和声明人，不去读仓库。

不带 `-git` 时，来源记为上传，路径是文件在文件夹内的路径。

## GitHub Actions 示例

```yaml
on:
  push:
    branches: [main]
    paths: ['contracts/shipments/**']
jobs:
  import:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Import the contract into Fabric
        run: fabric-import -product cargopai -asset shipments-api -dir contracts/shipments -git
        env:
          IDP_FABRIC_API_URL: https://fabric.example
          IDP_FABRIC_TOKEN: ${{ secrets.FABRIC_IMPORT_TOKEN }}
```

`fabric-import` 由 Fabric 仓库构建（`go build ./cmd/fabric-import`），放进 CI 镜像或作为制品下载。导入后，作者在门户里运行检查并准备 Release。
