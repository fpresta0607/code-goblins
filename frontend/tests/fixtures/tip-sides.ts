import { watchTips } from "../../src/tips";
import "../../src/styles.css";

// One part with a tip, which a test moves and sizes to leave its tip room on
// some sides of it and none on others. It stands in the board's canvas, where
// tips show, which draws no box of its own around it.
const canvas = document.createElement("main");
canvas.className = "canvas-region";
canvas.style.display = "contents";
const part = document.createElement("button");
part.id = "part";
part.className = "icon-button";
part.setAttribute("aria-label", "Pause");
part.setAttribute("data-tip", "Pause this goblin at its next stopping point");
part.style.position = "fixed";
canvas.append(part);
document.body.append(canvas);
watchTips();
