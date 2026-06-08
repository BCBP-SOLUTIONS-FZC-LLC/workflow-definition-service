// Finds fenced code blocks with class "language-mermaid" (produced by mkdocs
// from ```mermaid fences) and replaces them with rendered mermaid diagrams.
document.addEventListener('DOMContentLoaded', function () {
  if (typeof mermaid === 'undefined') return;

  mermaid.initialize({ startOnLoad: false, theme: 'default' });

  document.querySelectorAll('code.language-mermaid').forEach(function (el) {
    var pre = el.parentElement;          // <pre>
    var graphDef = el.textContent;
    var div = document.createElement('div');
    div.className = 'mermaid';
    div.textContent = graphDef;
    pre.parentNode.replaceChild(div, pre);
  });

  mermaid.run({ querySelector: '.mermaid' });
});
