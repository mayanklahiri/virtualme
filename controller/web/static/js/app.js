import { renderState, renderStatus } from "./render.js";
import { connect } from "./ws.js";

connect(renderState, renderStatus);
