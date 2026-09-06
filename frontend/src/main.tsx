import React from "react";
import ReactDOM from "react-dom/client";
import WebApp from "@twa-dev/sdk";
import App from "./App";
import "./style.css";
if (WebApp.initData) {
  WebApp.ready();
  WebApp.expand();
}
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
