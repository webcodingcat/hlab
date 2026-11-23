// The part of noVNC 1.7.0 the console window uses; noVNC ships no types
// (E22).
declare module "@novnc/novnc" {
  export type RFBOptions = {
    credentials?: { username?: string; password?: string; target?: string };
    shared?: boolean;
    wsProtocols?: string[];
  };

  export default class RFB extends EventTarget {
    /** urlOrChannel is a ws:// URL or a WebSocket that is connecting or open. */
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions);
    /** scaleViewport scales the screen to the size of the target element. */
    scaleViewport: boolean;
    background: string;
    focusOnClick: boolean;
    sendCtrlAltDel(): void;
    disconnect(): void;
    focus(options?: FocusOptions): void;
    // Events: "connect", "disconnect" (detail.clean), "securityfailure"
    // (detail.status, detail.reason), "credentialsrequired", "desktopname".
  }
}
