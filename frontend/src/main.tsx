import React from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import App from "./App";

document.documentElement.classList.remove("dark");
document.documentElement.dataset.theme = "light";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
