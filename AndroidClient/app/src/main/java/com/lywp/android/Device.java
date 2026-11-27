package com.lywp.android;

import android.content.Context;
import android.content.res.Configuration;

/** Detect phone vs tablet using resource qualifiers plus screen layout. */
final class Device {
    static boolean isTablet(Context ctx) {
        if (ctx.getResources().getBoolean(R.bool.is_tablet)) return true;
        int layout = ctx.getResources().getConfiguration().screenLayout & Configuration.SCREENLAYOUT_SIZE_MASK;
        return layout >= Configuration.SCREENLAYOUT_SIZE_LARGE;
    }

    static String label(Context ctx) {
        return ctx.getString(isTablet(ctx) ? R.string.device_tablet : R.string.device_phone);
    }

    private Device() {}
}
