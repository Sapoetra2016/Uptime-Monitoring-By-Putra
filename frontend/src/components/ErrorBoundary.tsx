import { Component, type ErrorInfo, type ReactNode } from "react";
import { t } from "../lib/i18n";

interface Props {
  children: ReactNode;
  /** Optional fallback UI shown when a descendant throws during render. */
  fallback?: ReactNode;
}

interface State {
  error: Error | null;
  info: ErrorInfo | null;
}

/**
 * Catches any render-time crash inside the wrapped subtree so a broken page
 * doesn't blank the whole app. Renders a friendly error card with the message
 * plus a "Reload" button. This is a top-level safety net; individual pages
 * already guard expected failure paths (fetch errors, missing data).
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, info: null };

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Keep it visible in the console for debugging without crashing the app.
    console.error("UI crashed:", error, info.componentStack);
    this.setState({ info });
  }

  reset = () => this.setState({ error: null, info: null });

  render() {
    if (!this.state.error) return this.props.children;
    if (this.props.fallback) return this.props.fallback;

    const err = this.state.error;
    // Extract just the crashing component names from React's componentStack so
    // it's readable at a glance.
    const stack = (this.state.info?.componentStack || "")
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l.startsWith("at "))
      .slice(0, 8)
      .join("\n");
    return (
      <div className="min-h-screen flex items-center justify-center p-6">
        <div className="bg-app-panel border border-slate-800 rounded-lg p-6 max-w-2xl w-full space-y-4">
          <h2 className="text-lg font-semibold text-app-down">{t("eb.title")}</h2>
          <p className="text-sm text-slate-300">{t("eb.desc")}</p>
          <pre className="text-xs bg-slate-900 border border-slate-800 rounded p-3 overflow-auto max-h-32 text-slate-400 whitespace-pre-wrap">
            {err.message || String(err)}
          </pre>
          {stack && (
            <details className="text-xs text-slate-400">
              <summary className="cursor-pointer">{t("eb.stack")}</summary>
              <pre className="mt-2 bg-slate-900 border border-slate-800 rounded p-3 overflow-auto max-h-48 whitespace-pre-wrap">
                {stack}
              </pre>
            </details>
          )}
          <div className="flex gap-2">
            <button className="btn-secondary" onClick={this.reset}>
              {t("eb.retry")}
            </button>
            <button
              className="btn-secondary"
              onClick={() => {
                window.location.href = "/";
              }}
            >
              {t("eb.backHome")}
            </button>
            <button
              className="btn-primary"
              onClick={() => window.location.reload()}
            >
              {t("eb.reload")}
            </button>
          </div>
        </div>
      </div>
    );
  }
}

export default ErrorBoundary;
