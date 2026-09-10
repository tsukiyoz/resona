import { build } from "esbuild";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const dir = fileURLToPath(new URL(".", import.meta.url));
const mineradio = process.argv[2] === "mineradio";
const dark = mineradio || process.argv[2] === "antd-dark";
const variant = dark || process.argv[2] === "antd" ? "antd" : "prototype";
const output = mineradio ? "mineradio.html" : dark
  ? "antd-dark.html"
  : variant === "antd"
    ? "antd.html"
    : "index.html";
const result = await build({
  entryPoints: [dir + variant + ".jsx"],
  bundle: true,
  write: false,
  minify: true,
  define: { "process.env.NODE_ENV": '"production"' },
  loader: { ".png": "dataurl" },
  nodePaths: [dir + "node_modules"],
});
const css =
  (await readFile(dir + variant + ".css", "utf8")) +
  (dark ? await readFile(dir + "antd-dark.css", "utf8") : "") +
  (mineradio ? (await readFile(dir + "mineradio.css", "utf8")) + (await readFile(dir + "mineradio-stage.css", "utf8")) : "");
await writeFile(
  dir + output,
  '<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Resona · UI 原型 01</title><style>' +
    css +
    "</style></head><body" +
    (mineradio ? ' data-variant="mineradio"' : dark ? ' data-variant="dark"' : "") +
    '><div id="root"></div><script>' +
    result.outputFiles[0].text.replaceAll("</script", "<\\/script") +
    "</script></body></html>",
);
console.log("Built standalone prototype: " + dir + output);
