// Safe contract fixtures: "<img src=x onerror=…>" remains text and
// "[x](javascript:alert(1))" remains text. No HTML parsing is used.
function appendInline(parent, text) {
  const token = /(`[^`\n]+`|\*\*[^*\n]+\*\*|\*[^*\n]+\*|\[[^\]\n]+\]\([^\s)\n]+\))/g;
  let offset = 0;
  for (const match of text.matchAll(token)) {
    parent.append(document.createTextNode(text.slice(offset, match.index)));
    const value = match[0];
    let element;
    if (value.startsWith("`")) {
      element = document.createElement("code");
      element.textContent = value.slice(1, -1);
    } else if (value.startsWith("**")) {
      element = document.createElement("strong");
      element.textContent = value.slice(2, -2);
    } else if (value.startsWith("*")) {
      element = document.createElement("em");
      element.textContent = value.slice(1, -1);
    } else {
      const link = value.match(/^\[([^\]]+)\]\((https?:\/\/[^)]+)\)$/);
      if (link) {
        element = document.createElement("a");
        element.textContent = link[1];
        element.href = link[2];
        element.target = "_blank";
        element.rel = "noopener noreferrer";
      } else {
        element = document.createTextNode(value);
      }
    }
    parent.append(element);
    offset = (match.index ?? 0) + value.length;
  }
  parent.append(document.createTextNode(text.slice(offset)));
}

function copyButton(text) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "code-copy";
  button.textContent = "Copy";
  button.addEventListener("click", async () => {
    await navigator.clipboard.writeText(text);
    button.textContent = "Copied";
    setTimeout(() => { button.textContent = "Copy"; }, 1500);
  });
  return button;
}

export function renderMarkdown(text) {
  const fragment = document.createDocumentFragment();
  const lines = String(text).replaceAll("\r\n", "\n").split("\n");
  for (let i = 0; i < lines.length;) {
    const line = lines[i];
    if (line.startsWith("```")) {
      const content = [];
      i++;
      while (i < lines.length && !lines[i].startsWith("```")) {
        content.push(lines[i++]);
      }
      if (i < lines.length) {
        i++;
      }
      const wrap = document.createElement("div");
      wrap.className = "code-block";
      const pre = document.createElement("pre");
      const code = document.createElement("code");
      code.textContent = content.join("\n");
      pre.append(code);
      wrap.append(copyButton(content.join("\n")), pre);
      fragment.append(wrap);
      continue;
    }
    const heading = line.match(/^(#{1,3}) (.+)$/);
    if (heading) {
      const element = document.createElement(`h${heading[1].length + 2}`);
      appendInline(element, heading[2]);
      fragment.append(element);
      i++;
      continue;
    }
    const list = line.match(/^(- |\* |1\. )(.+)$/);
    if (list) {
      const ordered = list[1] === "1. ";
      const element = document.createElement(ordered ? "ol" : "ul");
      const marker = ordered ? /^\d+\. (.+)$/ : /^[-*] (.+)$/;
      while (i < lines.length) {
        const item = lines[i].match(marker);
        if (!item) {
          break;
        }
        const li = document.createElement("li");
        appendInline(li, item[1]);
        element.append(li);
        i++;
      }
      fragment.append(element);
      continue;
    }
    if (line.trim() === "") {
      i++;
      continue;
    }
    const paragraph = [];
    while (i < lines.length && lines[i].trim() !== "" && !lines[i].startsWith("```") &&
      !/^(#{1,3}) /.test(lines[i]) && !/^(- |\* |\d+\. )/.test(lines[i])) {
      paragraph.push(lines[i++]);
    }
    if (paragraph.length === 0) {
      const literal = document.createElement("p");
      appendInline(literal, lines[i++]);
      fragment.append(literal);
    } else {
      const element = document.createElement("p");
      appendInline(element, paragraph.join("\n"));
      fragment.append(element);
    }
  }
  return fragment;
}
