package com.lywp.android;

import android.content.Context;
import android.graphics.Color;
import android.view.LayoutInflater;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.LinearLayout;
import android.widget.TextView;

/** Grid adapter for the native skin picker. */
final class SkinAdapter extends BaseAdapter {
    private final Context ctx;
    private final LayoutInflater inflater;
    private String selected;

    SkinAdapter(Context ctx, String selected) {
        this.ctx = ctx;
        this.inflater = LayoutInflater.from(ctx);
        this.selected = selected;
    }

    void setSelected(String id) { this.selected = id; }

    @Override public int getCount() { return Skins.ALL.length; }
    @Override public Object getItem(int position) { return Skins.ALL[position]; }
    @Override public long getItemId(int position) { return position; }

    @Override
    public View getView(int position, View convertView, ViewGroup parent) {
        View v = convertView;
        ViewHolder h;
        if (v == null) {
            v = inflater.inflate(R.layout.item_skin, parent, false);
            h = new ViewHolder();
            h.item = v.findViewById(R.id.skinItem);
            h.swatch = v.findViewById(R.id.skinSwatch);
            h.name = v.findViewById(R.id.skinName);
            h.desc = v.findViewById(R.id.skinDesc);
            h.check = v.findViewById(R.id.skinCheck);
            v.setTag(h);
        } else {
            h = (ViewHolder) v.getTag();
        }
        Skins.Skin skin = Skins.ALL[position];
        h.swatch.setBackgroundColor(skin.color);
        h.name.setText(skin.name + "  ·  " + skin.group);
        h.desc.setText(skin.desc);
        h.check.setVisibility(skin.id.equals(selected) ? View.VISIBLE : View.GONE);
        h.item.setBackgroundResource(skin.id.equals(selected)
                ? R.drawable.skin_chip : R.drawable.skin_chip);
        int border = skin.id.equals(selected) ? skin.color : Color.parseColor("#2A3550");
        android.graphics.drawable.GradientDrawable gd =
                (android.graphics.drawable.GradientDrawable) h.item.getBackground().mutate();
        gd.setStroke(2, border);
        return v;
    }

    private static final class ViewHolder {
        LinearLayout item;
        View swatch;
        TextView name;
        TextView desc;
        TextView check;
    }
}
