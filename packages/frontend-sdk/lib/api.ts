export type StreamType = "desktop" | "webcam";

export type IOfferReceiveResponder = (playerId: string, offer: string, streamType: StreamType) => Promise<string>;

