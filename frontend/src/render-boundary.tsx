import { Component, type ReactNode } from "react";
import { Icon } from "./Icon";

export class RenderBoundary extends Component<{ scope: "card" | "list"; children: ReactNode }, { hasFailure: boolean }> {
  state = { hasFailure: false };

  static getDerivedStateFromError() {
    return { hasFailure: true };
  }

  render() {
    if (!this.state.hasFailure) return this.props.children;
    return <div className="error-box render-error" role="alert">
      <Icon name="warning" />
      <span>This {this.props.scope} could not be shown.</span>
      <button className="icon-button raised" aria-label="Retry" data-tip="Retry" data-tip-align="end" onClick={() => this.setState({ hasFailure: false })}><Icon name="refresh" /></button>
    </div>;
  }
}
