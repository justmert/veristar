// class merging that knows the brand scale. the stock merge only knows
// tailwind's default names, so it read `text-metric` as a colour and dropped
// it next to `text-fg`; here every type step, radius and shadow token is
// registered in its group.
import { createCn } from "cn/config";
import { radius, shadow, type } from "./tokens";

export const cn = createCn({
  extend: {
    classGroups: {
      "font-size": [{ text: Object.keys(type) }],
      rounded: [{ rounded: Object.keys(radius) }],
      shadow: [{ shadow: Object.keys(shadow) }],
    },
  },
});
