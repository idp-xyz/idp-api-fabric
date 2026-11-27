import { api, button, field, icon, node, productPath } from "./workbench.js";

// What importRevision takes: at most 16 files and 40960 decoded bytes.
const maxFiles = 16;
const maxBytes = 40960;
const sourcePath = /^[a-zA-Z0-9._-]+(\/[a-zA-Z0-9._-]+)*$/;
const contractFile = /\.(ya?ml|json)$/i;
const entryNames = ["openapi.yaml", "openapi.yml", "openapi.json"];
const keyPattern = "[a-z][a-z0-9\\-]{0,61}[a-z0-9]";

const size = bytes => bytes < 1024 ? bytes + " B" : (bytes / 1024).toFixed(1) + " KB";

function base64(bytes) {
  let text = "";
  for (let start = 0; start < bytes.length; start += 0x8000) {
    text += String.fromCharCode(...bytes.subarray(start, start + 0x8000));
  }
  return btoa(text);
}

function textInput(maxLength, placeholder = "") {
  const input = node("input");
  input.required = true;
  input.maxLength = maxLength;
  input.autocomplete = "off";
  input.spellcheck = false;
  if (placeholder) input.placeholder = placeholder;
  return input;
}

function say(status, text, error = false) {
  status.textContent = text;
  status.classList.toggle("publication-error", error);
}

// assetForm registers an API asset in a workspace, for the maintainers of
// the workspace. The workspace owns the asset, and the server gives it its
// identifier and the lifecycle it starts in.
export function assetForm({ product, workspace, signal, onRegistered }) {
  const name = textInput(120, "如 Shipments API");
  const key = textInput(63, "如 shipments-api");
  key.pattern = keyPattern;
  key.title = "小写字母、数字和连字符，2–63 个字符，以字母开头，不以连字符结尾";
  const domain = textInput(63, "如 shipping");
  domain.pattern = keyPattern;
  domain.title = key.title;
  const kind = node("select");
  kind.add(new Option("HTTP", "HTTP"));
  kind.add(new Option("事件", "EVENT"));
  const submit = node("button", "", "primary");
  submit.type = "submit";
  submit.append(icon("asset"), node("span", "登记资产"));
  const status = node("p", "", "status-line");
  status.setAttribute("role", "status");
  const fields = node("div", "", "form operation-form import-fields");
  fields.append(field("名称", name, "asset-name"), field("资产键", key, "asset-key"), field("领域键", domain, "asset-domain"),
    field("类型", kind, "asset-kind"));
  const form = node("form", "", "form import-form");
  form.append(fields, node("p", "资产键在产品内唯一，登记后不能修改。资产属于当前 Workspace，从“试验”生命周期开始。", "hint"), submit, status);
  form.onsubmit = async event => {
    event.preventDefault();
    submit.disabled = true;
    say(status, "正在登记…");
    try {
      const asset = await api(productPath(product) + "/assets", {
        signal,
        data: { workspace_id: workspace, asset_key: key.value, display_name: name.value.trim(), domain_key: domain.value, api_kind: kind.value },
      });
      if (signal.aborted) return;
      form.reset();
      say(status, "");
      onRegistered(asset);
    } catch (error) {
      if (!signal.aborted) say(status, error.message, true);
    } finally {
      if (!signal.aborted) submit.disabled = false;
    }
  };
  return form;
}

// importForm imports OpenAPI files as a draft revision of an HTTP asset, for
// the authors of its workspace. Files picked one by one are named by their
// file names; the files of a folder keep their paths inside it, so that the
// relative references between them resolve. Only YAML and JSON files are
// taken, and none under a hidden folder. The source is recorded as an upload:
// a Git origin is declared by whoever imports from the repository, such as
// CI, through the API.
export function importForm({ path, signal, onImported }) {
  const filePicker = node("input");
  filePicker.type = "file";
  filePicker.multiple = true;
  filePicker.accept = ".yaml,.yml,.json";
  const folderPicker = node("input");
  folderPicker.type = "file";
  folderPicker.webkitdirectory = true;
  for (const picker of [filePicker, folderPicker]) {
    picker.hidden = true;
    picker.tabIndex = -1;
  }
  const pickFiles = button("选择文件", "secondary", "file");
  const pickFolder = button("选择文件夹", "secondary", "folder");
  pickFiles.onclick = () => filePicker.click();
  pickFolder.onclick = () => folderPicker.click();
  const pickers = node("div", "", "control-row");
  pickers.append(pickFiles, pickFolder, filePicker, folderPicker);

  const listing = node("ul", "", "import-files");
  listing.setAttribute("aria-label", "要导入的文件");
  const totals = node("p", "", "hint");
  const problems = node("ul", "", "import-problems");
  const entry = node("select");
  const entryField = field("入口文件", entry, "import-entry");
  const submit = node("button", "", "primary");
  submit.type = "submit";
  submit.append(icon("publish"), node("span", "导入为新修订"));
  const status = node("p", "", "status-line");
  status.setAttribute("role", "status");
  const form = node("form", "", "form import-form");
  form.append(pickers,
    node("p", "选择 OpenAPI 3.1.2 的 YAML 或 JSON 文件。契约分成多个文件、彼此相对引用时，选择包含它们的文件夹。最多 " + maxFiles +
      " 个文件，合计 " + maxBytes / 1024 + " KB。", "hint"),
    totals, listing, problems, entryField, submit, status);

  let files = [];
  let reading = false;
  sync();

  function sync() {
    const faults = [];
    const bytes = files.reduce((sum, item) => sum + item.bytes.length, 0);
    if (files.length > maxFiles) faults.push("文件超过 " + maxFiles + " 个。");
    if (bytes > maxBytes) faults.push("文件合计超过 " + maxBytes / 1024 + " KB。");
    for (const item of files) {
      if (!sourcePath.test(item.path) || item.path.length > 200) faults.push(item.path + "：路径只能用字母、数字、点、下划线和连字符。");
      if (item.text === null) faults.push(item.path + "：不是 UTF-8 文本。");
    }
    problems.replaceChildren(...faults.map(text => node("li", text)));
    totals.textContent = files.length ? files.length + " 个文件，合计 " + size(bytes) + "。" : "";
    entryField.hidden = !files.length;
    submit.disabled = reading || !files.length || faults.length > 0 || !entry.value;
    filePicker.disabled = folderPicker.disabled = pickFiles.disabled = pickFolder.disabled = reading;
  }

  // The entrypoint is the file named openapi at the top, else the one file
  // that declares an OpenAPI version, else the first.
  function guessEntry() {
    const top = files.find(item => entryNames.includes(item.path));
    const declared = files.filter(item => /^(openapi\s*:|\s*\{?\s*"openapi"\s*:)/m.test(item.text || ""));
    return (top || (declared.length === 1 ? declared[0] : files[0]))?.path || "";
  }

  async function take(chosen, fromFolder) {
    reading = true;
    sync();
    say(status, "正在读取文件…");
    try {
      const picked = [];
      let skipped = 0;
      for (const file of chosen) {
        const name = fromFolder ? file.webkitRelativePath.split("/").slice(1).join("/") : file.name;
        if (!contractFile.test(name) || name.split("/").some(part => part.startsWith("."))) {
          skipped += 1;
          continue;
        }
        picked.push({ path: name, file });
      }
      const total = picked.reduce((sum, item) => sum + item.file.size, 0);
      // Too many or too big is said without reading them all.
      if (picked.length > maxFiles || total > maxBytes) {
        files = picked.map(item => ({ path: item.path, bytes: { length: item.file.size }, text: "" }));
      } else {
        const decoder = new TextDecoder("utf-8", { fatal: true });
        files = await Promise.all(picked.map(async item => {
          const bytes = new Uint8Array(await item.file.arrayBuffer());
          let text = null;
          try {
            text = decoder.decode(bytes);
          } catch {
            // Reported by sync.
          }
          return { path: item.path, bytes, text };
        }));
      }
      if (signal.aborted) return;
      files.sort((a, b) => a.path.localeCompare(b.path));
      listing.replaceChildren(...files.map(item => {
        const line = node("li");
        line.append(node("code", item.path, "digest"), node("span", size(item.bytes.length), "muted"));
        return line;
      }));
      entry.replaceChildren(...files.map(item => new Option(item.path, item.path)));
      entry.value = guessEntry();
      const rule = "只导入 YAML 和 JSON 文件，不含隐藏文件夹里的文件。";
      say(status, files.length ? (skipped ? "已略过 " + skipped + " 个文件：" + rule : "") : "没有可导入的文件：" + rule, !files.length);
    } catch (error) {
      if (!signal.aborted) say(status, "读取文件失败：" + error.message, true);
    } finally {
      reading = false;
      if (!signal.aborted) sync();
    }
  }

  filePicker.onchange = () => take([...filePicker.files], false).finally(() => { filePicker.value = ""; });
  folderPicker.onchange = () => take([...folderPicker.files], true).finally(() => { folderPicker.value = ""; });
  entry.onchange = sync;

  // A response lost on the way is answered the same when the files are sent
  // again: the revision recorded from them, with 200.
  form.onsubmit = async event => {
    event.preventDefault();
    if (submit.disabled) return;
    submit.disabled = true;
    say(status, "正在导入…");
    try {
      const data = { entrypoint: entry.value, files: files.map(item => ({ path: item.path, content_base64: base64(item.bytes) })) };
      const { status: answered, body } = await api(path, { signal, data, withStatus: true });
      if (signal.aborted) return;
      onImported(body, answered === 201);
    } catch (error) {
      if (!signal.aborted) say(status, error.message, true);
    } finally {
      if (!signal.aborted) sync();
    }
  };
  return form;
}
