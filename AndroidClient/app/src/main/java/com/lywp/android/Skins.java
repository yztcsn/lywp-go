package com.lywp.android;

/** Skin catalogue mirrored from the Go edition (22 skins). */
final class Skins {
    static final class Skin {
        final String id;
        final String name;
        final String desc;
        final String group;
        final int color;

        Skin(String id, String name, String desc, String group, int color) {
            this.id = id;
            this.name = name;
            this.desc = desc;
            this.group = group;
            this.color = color;
        }
    }

    static final Skin[] ALL = {
        new Skin("ocean", "深海蓝", "默认 · 蓝紫微光", "配色", 0xFF7AA2FF),
        new Skin("emerald", "翡翠绿", "清透森林绿", "配色", 0xFF34D399),
        new Skin("amber", "琥珀金", "暖调琥珀黄", "配色", 0xFFF5B942),
        new Skin("rose", "玫瑰粉", "柔和莓果红", "配色", 0xFFFB7185),
        new Skin("violet", "极光紫", "深空紫罗兰", "配色", 0xFFA78BFA),
        new Skin("square", "方寸", "直角 + 纯色面板", "界面", 0xFF60A5FA),
        new Skin("pill", "胶囊", "全圆角胶囊按钮", "界面", 0xFFF472B6),
        new Skin("outline", "霓虹描边", "透明描边发光按钮", "界面", 0xFF38BDF8),
        new Skin("flat", "极简平面", "无阴影小圆角", "界面", 0xFF94A3B8),
        new Skin("raised", "立体浮雕", "凸起拟物按钮", "界面", 0xFF818CF8),
        new Skin("tech", "科幻切角", "斜切直角按钮", "界面", 0xFF22D3EE),
        new Skin("mono", "黑白灰", "高对比反色按钮", "界面", 0xFFFFFFFF),
        new Skin("paper", "素白直角", "亮色纯白直角", "界面", 0xFFF8FAFC),
        new Skin("slate", "石板直角", "冷灰青色直角", "界面", 0xFF64748B),
        new Skin("lime", "青柠直角", "亮色青柠直角", "界面", 0xFFA3E635),
        new Skin("cloud", "云白", "浅蓝清爽亮色", "亮色", 0xFF60A5FA),
        new Skin("cream", "奶油", "暖黄柔和亮色", "亮色", 0xFFFCD34D),
        new Skin("mint", "薄荷", "浅绿清新亮色", "亮色", 0xFF6EE7B7),
        new Skin("frost", "冰晶玻璃", "亮色强毛玻璃", "毛玻璃", 0xFFBAE6FD),
        new Skin("aurora", "极光玻璃", "暗色柔光毛玻璃", "毛玻璃", 0xFFA78BFA),
        new Skin("smoke", "烟灰玻璃", "中性灰毛玻璃", "毛玻璃", 0xFF94A3B8),
        new Skin("glass-square", "冰晶直角", "亮色毛玻璃 + 直角", "毛玻璃", 0xFF7DD3FC),
    };

    static String nameOf(String id) {
        for (Skin s : ALL) if (s.id.equals(id)) return s.name;
        return "深海蓝";
    }

    private Skins() {}
}
