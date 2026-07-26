// @ts-nocheck

class FakeElement {
  constructor(tag = "div") {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.listeners = new Map();
    this.className = "";
    this.textContent = "";
    this.value = "";
    this.hidden = false;
    this.disabled = false;
    this.focused = false;
    this.dataset = {};
    this.validationMessage = "";
    this.classList = {
      add: (...names) => {
        for (const name of names) {
          if (!this.className.split(" ").includes(name)) this.className = `${this.className} ${name}`.trim();
        }
      },
      remove: (...names) => {
        this.className = this.className.split(" ").filter((name) => !names.includes(name)).join(" ");
      },
      contains: (name) => this.className.split(" ").includes(name),
    };
  }

  get options() { return this.children; }

  append(...children) { this.children.push(...children); }

  replaceChildren(...children) { this.children = [...children]; }

  addEventListener(type, listener) { this.listeners.set(type, listener); }

  dispatch(type) { return this.listeners.get(type)?.({ preventDefault() {} }); }

  querySelector() { return null; }

  focus() { this.focused = true; }

  setCustomValidity(message) { this.validationMessage = message; }
}

export function createFakeDOM(selectors) {
  const nodes = new Map(selectors.map((selector) => [selector, new FakeElement()]));
  const body = new FakeElement("body");
  return {
    nodes,
    body,
    document: {
      body,
      querySelector: (selector) => nodes.get(selector) ?? null,
      createElement: (tag) => new FakeElement(tag),
    },
  };
}
