import { Component, type ErrorInfo, type ReactNode } from "react";
import { message, reportToCfo } from "./api";
import { Icon } from "./Icon";

// A card or a list that could not be drawn: the CFO hears why, and the board
// shows only a Retry in its place.
export class RenderBoundary extends Component<{ scope: "card" | "list"; children: ReactNode }, { hasFailure: boolean }> {
  state = { hasFailure: false };

  static getDerivedStateFromError() {
    return { hasFailure: true };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    reportToCfo("drawing a " + this.props.scope + " on the board", message(error) + (info.componentStack ? "\n" + info.componentStack.trim().split("\n").slice(0, 3).join("\n") : ""));
  }

  render() {
    if (!this.state.hasFailure) return this.props.children;
    return <div className="render-error">
      <button className="icon-button raised" aria-label={"Show this " + this.props.scope + " again"} data-tip="Retry" data-tip-align="end" onClick={() => this.setState({ hasFailure: false })}><Icon name="refresh" /></button>
    </div>;
  }
}
