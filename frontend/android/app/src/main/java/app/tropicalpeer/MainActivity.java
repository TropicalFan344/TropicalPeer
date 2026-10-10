package app.tropicalpeer;

import android.os.Bundle;

import com.getcapacitor.BridgeActivity;

public class MainActivity extends BridgeActivity {
    @Override
    public void onCreate(Bundle savedInstanceState) {
        registerPlugin(TropicalPeerPlugin.class);
        super.onCreate(savedInstanceState);
    }
}
