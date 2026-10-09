import { watchTips } from "../../src/tips";
import "../../src/styles.css";

// One part with a tip, which a test moves and sizes to leave its tip room on
// some sides of it and none on others.
const part = document.createElement("button");
part.id = "part";
part.className = "icon-button";
part.setAttribute("aria-label", "Pause");
part.setAttribute("data-tip", "Pause this goblin at its next stopping point");
part.style.position = "fixed";
document.body.append(part);
watchTips();
