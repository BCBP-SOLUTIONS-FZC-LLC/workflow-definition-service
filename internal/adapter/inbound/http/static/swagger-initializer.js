window.onload = function() {
  const KeepModelTogglePlugin = function() {
    return {
      wrapComponents: {
        ModelCollapse: function(Original, system) {
          return function(props) {
            var next = Object.assign({}, props);
            // Nested property rows use "[...]" — keep the collapse toggle after expand.
            // Top-level schemas use "{...}" — preserve hideSelfOnExpand to avoid duplicate titles.
            if (props.collapsedContent === '[...]') {
              next.hideSelfOnExpand = false;
            }
            return system.React.createElement(Original, next);
          };
        }
      }
    };
  };

  const ui = SwaggerUIBundle({
    url: "/api/openapi.yaml",
    dom_id: '#swagger-ui',
    validatorUrl: null,
    oauth2RedirectUrl: `${window.location.protocol}//${window.location.host}${window.location.pathname.split('/').slice(0, window.location.pathname.split('/').length - 1).join('/')}/oauth2-redirect.html`,
    persistAuthorization: false,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [
      SwaggerUIBundle.plugins.DownloadUrl,
      KeepModelTogglePlugin
    ],
    layout: "StandaloneLayout",
    docExpansion: "list",
    deepLinking: true,
    defaultModelsExpandDepth: 1
  });

  window.ui = ui;

  // Inject light/dark theme toggle into the topbar.
  (function injectThemeToggle() {
    function applyTheme(theme) {
      document.documentElement.setAttribute('data-theme', theme);
      var btn = document.getElementById('swagger-theme-toggle');
      if (btn) btn.textContent = theme === 'light' ? '☀️ Light' : '🌙 Dark';
      try { localStorage.setItem('swagger-theme', theme); } catch(e) {}
    }

    function addToggleButton() {
      var topbar = document.querySelector('.swagger-ui .topbar');
      if (!topbar) { setTimeout(addToggleButton, 200); return; }
      if (document.getElementById('swagger-theme-toggle')) return;

      var btn = document.createElement('button');
      btn.id = 'swagger-theme-toggle';
      btn.style.cssText = 'margin-left:auto;margin-right:1rem;background:none;border:1px solid rgba(132,38,176,.5);color:#9ca3af;border-radius:20px;padding:4px 12px;font-size:.78rem;cursor:pointer;font-family:"Poppins",sans-serif;white-space:nowrap;transition:border-color .15s,color .15s';
      btn.onmouseover = function(){ this.style.borderColor='#bd0283'; this.style.color='#fff'; };
      btn.onmouseout  = function(){ this.style.borderColor='rgba(132,38,176,.5)'; this.style.color='#9ca3af'; };
      btn.onclick = function() {
        var cur = document.documentElement.getAttribute('data-theme') || 'dark';
        applyTheme(cur === 'light' ? 'dark' : 'light');
      };
      topbar.appendChild(btn);

      var saved;
      try { saved = localStorage.getItem('swagger-theme'); } catch(e) {}
      applyTheme(saved === 'light' ? 'light' : 'dark');
    }

    addToggleButton();
  })();
};
